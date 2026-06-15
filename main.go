package main

/**
 * Author: ApardoO
 * Project Name: Raspverry Pi Reverse Proxy (With Docker)
 * Version: 1.2
 * */

import (
	"gopkg.in/yaml.v3"
	
	"github.com/docker/docker/api/types/container"
	"github.com/docker/docker/api/types/filters"
	"github.com/docker/docker/api/types/image"
	"github.com/docker/docker/client"
	"github.com/docker/go-connections/nat"

	"net/http/httputil"
	"encoding/json"
	"os/signal"
	"net/http"
	"net/url"
	"os/exec"
	"syscall"
	"context"
	"strings"
	"sync"
	"time"
	"net"
	"fmt"
	"log"
	"os"
	"io"
)


// ===========================================
//      TIPOS DE DATOS PARA CONFIGURACIÓN
// ===========================================

// >>> Archivo de configuración <<<
type Config struct {
	Name      string  `yaml:"name"`      // Nombre del servicio
	Version   float64 `yaml:"version"`   // Version del servicio
	Port      int     `yaml:"port"`      // Puerto por defecto a ponerse en escucha
	Protocol  string  `yaml:"protocol"`  // Protocolo por el cual se lanzará
	Endpoints string  `yaml:"endpoints"` // Archivo de endpoints a configurar
}

// >>> Archivo de configuración <<<
type DockerConfig struct {
	Type        string   `json:"type"`         // "docker" | "compose"
	Path        string   `json:"path"`         // Imagen: dir con Dockerfile | Compose: ruta al .yml
	Image       string   `json:"image"`        // Solo type "docker": imagen a usar/construir
	Args        []string `json:"args"`         // Variables de entorno extra: ["CLAVE=valor"]
	IdleTimeout string   `json:"idle_timeout"` // "30m", "2h" — formato time.ParseDuration
	Persist     bool     `json:"persist"`      // true → nunca se apaga por inactividad
}

// >>> Archivo de endpoints <<<
type Target struct {
	Protocol string `json:"protocol"` // Protocolo
	Ip       string `json:"ip"`       // Dirección IP a la que resuelve
	Port     int    `json:"port"`     // Puerto en el que se ha configurado el servicio
	Standar  int    `json:"standar"`  // Puerto estándar en el que normalmente resuelve el servicio
}

type Route struct {
	Endpoint string        `json:"endpoint"`         // Nombre host a redireccionar
	Docker   *DockerConfig `json:"docker,omitempty"` // Configuración del docker en caso de que haya servicios montados con docker
	Target   Target        `json:"target"`           // Configuración del endpoint a redirigir
}



// ======================================
//      FUNCONALIDADES DE UTILIDADES
// ======================================

// [*] Lectura del archivo de configuración
func ReadConfigFile(path string) (Config, error) {
	// Lectura del archivo
	data, err := os.ReadFile(path)
	if err != nil { return Config{}, err }

	// Estructura de la configuración
	var conf Config

	// Conversión al Config
	err = yaml.Unmarshal(data, &conf)
	if err != nil { return Config{}, err }

	return conf, nil
}

// [*] Lectura del archivo con los endpoints
func ReadRoutingFile(path string) ([]Route, error) {
	// Lectura del archivo
	data, err := os.ReadFile(path)
	if err != nil { return nil, err }

	// Estructura de los datos
	var routes []Route

	// Conversión de los datos a la estructura
	err = json.Unmarshal(data, &routes)
	if err != nil { return nil, err }

	return routes, nil
}

// [*] Nombre de contenedir predecible a partir del endpoint
// "ssh.lan" → "revpiprox-ssh-lan"
func containerName(endpoint string) string {
	return fmt.Sprintf("revpiprox-%s", strings.ReplaceAll(endpoint, ".", "-"))
}



// ====================================================
//      GESTIÓN DE DOCKER — SDK OFICIAL
// ====================================================

// Cliente SDK compartido (conexión persistente al daemon)
// Mantiene una única conexión persistente al daemon de Docker
// (para ahorrar el abrir y errar el subproceso por cada operacion)
type DockerManager struct {
	cli *client.Client  // Cliente HTTP que habla con el daemon (/var/run/docker.sock)
	ctx context.Context // Contexto base: se puede cancelar para abortar operaciones en curso
}

// Crea e inicializa un DockerManager
// - client.FromEnv hace igual que hace el CLI de docker
// - WithAPIVersionNegotiation negocia la version de la API
// Error si el daemon no está accesible
func NewDockerManager() (*DockerManager, error) {
	cli, err := client.NewClientWithOpts(
		client.FromEnv, // lee DOCKER_HOST, DOCKER_CERT_PATH Y DOCKER_TLS_VERIFY del entorno
		client.WithAPIVersionNegotiation(), // Negocia versión automáticamente con el daemon
	)
	if err != nil { return nil, fmt.Errorf("error conectando al daemon Docker: %v", err) }

	return &DockerManager{cli: cli, ctx: context.Background()}, nil
}

// Libera la conexión HTTP subyacente con el daemon
func (dm *DockerManager) Close() {
	dm.cli.Close()
}


// ── Helpers ──────────────────────────────────────────

// [+] Comprueba si el daemon conoce un contenedor con ese nombre
// El filtro por nombre evita recorrer todos los contenedores del sistema
func (dm *DockerManager) containerExists(name string) bool {
	f := filters.NewArgs(filters.Arg("name", name))
	// Obtiene la lista de posibles candidatos
	list, err := dm.cli.ContainerList(dm.ctx, container.ListOptions{
		All:     true, // incluye contenedores parados
		Filters: f,
	})
	if err != nil { return false }

	// Recorremos la lista para averiguar si existe el contenedor
	for _, c := range list {
		for _, n := range c.Names {
			// Si el contenedor existe, retornamos true
			if n == "/"+name { return true }
		}
	}

	// Si no se ha encontrado, retornamos false
	return false
}

// [+] Comprueba si una imagen ya está descargada en el daemon local
func (dm *DockerManager) imageExistsLocally(imageName string) bool {
	f := filters.NewArgs(filters.Arg("reference", imageName))
	list, err := dm.cli.ImageList(dm.ctx, image.ListOptions{Filters: f})
	return err == nil && len(list) > 0
}

// [+] Parsea ["8080:80", "5432:5432"] al formato que espera el SDK
// - nat.PortMap   → qué puerto del host mapea a qué puerto del contenedor
// - nat.PortSet   → qué puertos declara el contenedor como expuestos
func parsePortBindings(args []string) (nat.PortMap, nat.PortSet, error) {
	portBindings := nat.PortMap{}
	exposedPorts := nat.PortSet{}

	for _, arg := range args {
		parts := strings.SplitN(arg, ":", 2)
		if len(parts) != 2 {
			return nil, nil, fmt.Errorf("formato de puerto inválido '%s' (usa 'hostPort:containerPort')", arg)
		}
		hostPort, containerPort := parts[0], parts[1]
		p := nat.Port(containerPort + "/tcp")
		portBindings[p] = []nat.PortBinding{{HostPort: hostPort}}
		exposedPorts[p] = struct{}{}
	}

	return portBindings, exposedPorts, nil
}


// ── Lanzar ──────────────────────────────────────────

// [+] Compose: exec.Command (el daemon no expone API para esto)
func (dm *DockerManager) launchCompose(endpoint string, d *DockerConfig) error {
	// Comprobamos la ruta del archivo compose
	if d.Path == "" {
		return fmt.Errorf("[%s] docker.path es obligatorio para type 'compose'", endpoint)
	}
 
	log.Printf("[DOCKER/COMPOSE]: Lanzando '%s' desde '%s'...\n", endpoint, d.Path)
	cmd := exec.Command("docker", "compose", "-f", d.Path, "up", "-d")

	// Redirigimos el stdout/stderr al proceso padre para que los logs sean visibles
	cmd.Stdout = os.Stdout
	cmd.Stderr = os.Stderr
	return cmd.Run()
}

// [+] Docker SDK: pull (si hace falta) → build (si hay Dockerfile) → create → start
func (dm *DockerManager) launchContainer(endpoint string, d *DockerConfig) error {
	// Comprobamos que la imagen exista en la configuración
	if d.Image == "" {
		return fmt.Errorf("[%s] docker.image es obligatorio para type 'docker'", endpoint)
	}

	// Nombre del contenedor
	name := containerName(endpoint)

	// [1] El contenedor ya existe (puede estar parado tras un idle shutdown)
	if dm.containerExists(name) {
		log.Printf("[DOCKER]: Contenedor '%s' ya existe, arrancando...\n", name)
		return dm.cli.ContainerStart(dm.ctx, name, container.StartOptions{})
	}

	// Imagen local / remota
	if d.Path != "" {
		// [2] Hay un Dockerfile local → build primero
		log.Printf("[DOCKER]: Construyendo imagen '%s' desde '%s'...\n", d.Image, d.Path)
		cmd := exec.Command("docker", "build", "-t", d.Image, d.Path)
		cmd.Stdout = os.Stdout
		cmd.Stderr = os.Stderr
		if err := cmd.Run(); err != nil {
			return fmt.Errorf("[%s] docker build falló: %v", endpoint, err)
		}
	} else {
		// [3] Imagen remota → pull solo si no está ya en local
		if !dm.imageExistsLocally(d.Image) {
			log.Printf("[DOCKER]: Descargando imagen '%s'...\n", d.Image)
			reader, err := dm.cli.ImagePull(dm.ctx, d.Image, image.PullOptions{})
			if err != nil {
				return fmt.Errorf("[%s] error al hacer pull de '%s': %v", endpoint, d.Image, err)
			}

			// ImagePull devuelve un stream JSON con el progreso de cada capa,
			// hay que drenarlo completamente o el pull se cancela antes de terminar
			io.Copy(os.Stdout, reader)
			reader.Close()
		}
	}

	// Convertimos los args del endpoint ("hostPort:containerPort")
	// Formato esperado en args: ["8080:80", "5432:5432"]
	portBindings, exposedPorts, err := parsePortBindings(d.Args)
	if err != nil {
		return fmt.Errorf("[%s] error en port bindings: %v", endpoint, err)
	}

	// Registra el contenedor en el daemon pero no lo arranca
	resp, err := dm.cli.ContainerCreate(
		dm.ctx,
		&container.Config{
			Image:        d.Image,
			ExposedPorts: exposedPorts, // Puertos que el contenedor declara internamente
		},
		&container.HostConfig{
			PortBindings: portBindings, // Mapeo host:contenedor
			RestartPolicy: container.RestartPolicy{
				Name: "no", // El proxy controla el ciclo de vida
			},
		},
		nil, // NetworkingConfig
		nil, // Platform
		name,
	)
	if err != nil {
		return fmt.Errorf("[%s] error creando contenedor: %v", endpoint, err)
	}

	log.Printf("[DOCKER]: Contenedor '%s' creado (ID: %s), arrancando...\n", name, resp.ID[:12])
	return dm.cli.ContainerStart(dm.ctx, resp.ID, container.StartOptions{})
}

// [+] Punto de entrada para arrancar cualquier servicio Docker
// Actúa como dispatcher: delega el launchCompose o launchContainer
func (dm *DockerManager) LaunchService(endpoint string, d *DockerConfig) error {
	// Comprobar la configuración del docker
	if d == nil { return nil }

	switch strings.ToLower(d.Type) {
		// En caso de que se lance desde docker compose
		case "compose":
			return dm.launchCompose(endpoint, d)
		// En caso de que se lance desde docker normal
		case "docker":
			return dm.launchContainer(endpoint, d)
		default:
			return fmt.Errorf("[%s] docker.type desconocido: '%s'", endpoint, d.Type)
	}
}


// ── Parar ────────────────────────────────────────────

// [+] detiene el servicio asociado a un endpoint
func (dm *DockerManager) StopService(endpoint string, d *DockerConfig) error {
	switch strings.ToLower(d.Type) {
		case "compose":
			log.Printf("[DOCKER/COMPOSE]: Parando '%s'...\n", endpoint)
			cmd := exec.Command("docker", "compose", "-f", d.Path, "down")
			cmd.Stdout = os.Stdout
			cmd.Stderr = os.Stderr
			return cmd.Run()

		case "docker":
			name := containerName(endpoint)
			log.Printf("[DOCKER]: Parando contenedor '%s'...\n", name)
			timeout := 10
			return dm.cli.ContainerStop(dm.ctx, name, container.StopOptions{Timeout: &timeout})

		default:
			return fmt.Errorf("tipo docker desconocido: %s", d.Type)
	}
}


// ── Inspección ───────────────────────────────────────

// [+] Espera TCP al puerto del target (válido para compose y docker)
func (dm *DockerManager) WaitForTarget(endpoint string, target Target, timeout time.Duration) error {
	addr := fmt.Sprintf("%s:%d", target.Ip, target.Port) // Direccion destino
	deadline := time.Now().Add(timeout) // Fin de tiempo
	log.Printf("[DOCKER]: Esperando a que '%s' esté disponible en %s...\n", endpoint, addr)
 
 	// Cada 500ms se reintenta la conexión hasta que se llega al deadline
	for time.Now().Before(deadline) {
		conn, err := net.DialTimeout("tcp", addr, 1*time.Second)
		if err == nil {
			conn.Close()
			log.Printf("[DOCKER]: '%s' listo en %s ✓\n", endpoint, addr)
			return nil
		}
		time.Sleep(500 * time.Millisecond)
	}

	return fmt.Errorf("[%s] timeout esperando al servicio en %s", endpoint, addr)
}

// [+] consulta el estado real del contenedor directamente al daemon vía SDK
func (dm *DockerManager) IsContainerRunning(endpoint string) (bool, error) {
	name := containerName(endpoint)
	inspect, err := dm.cli.ContainerInspect(dm.ctx, name)
	if err != nil {
		// IsErrNotFound distingue "contenedor no existe" de otros errores de red/daemon
		if client.IsErrNotFound(err) { return false, nil }
		return false, err
	}
	return inspect.State.Running, nil
}



// =============================================
//      CONTROL DE ACTIVIDAD E INACTIVIDAD
// =============================================

// Representa el estado del contenedor desde el punto de vista del proxy
// Es distinto al estado que reporta el daemon. Importa si el proxy puede enrutar tráfico hacia el contenedor
type ContainerState int
 
const (
	StateRunning  ContainerState = iota // Contenedor activo y aceptando tráfico
	StateStopped                        // Contenedor apagado por inactividad
	StateStarting                       // Contenedor arrancano tras un wake-on-demand
)

// Almacena el estado de actividad de un único endpoint
// Tiene su propio mutex porque varios handlers HTTP pueden tocar el mismo endpoint concurrentemente
type RouteActivity struct {
	mu          sync.RWMutex   // Protege los campos de esta struct individualmente
	lastSeen    time.Time      // Timestamp de la última petición recibida
	state       ContainerState // Estado actual desde el punto de vista del proxy
	idleTimeout time.Duration  // Tiempo de inactividad hasta apagar (0 = nunca)
	persist     bool           // Si es true, nunca se apaga aunque supere el timeout
	route       Route          // Referencia a la configuración del endpoint (para el wake-on-deman)
}

// Registro central de actividad de todos los endpoints
type ActivityRegistry struct {
	mu     sync.RWMutex              // Protege el mapa routes
	routes map[string]*RouteActivity // Clave: nombre del endpoint
}


// [+] Crea un registro vacío listo para usar
func NewActivityRegistry() *ActivityRegistry {
	return &ActivityRegistry{routes: make(map[string]*RouteActivity)}
}

// [+] Añade un endpoint al registro
func (ar *ActivityRegistry) Register(endpoint string, d *DockerConfig, r Route) {
	// Si el endpoint no tiene una configuración de docker, nos lo saltamos
	if d == nil { return }
 
 	// Obtenemos el idle_timeout para no hacerlo en cada tick del watcher
	var idleTimeout time.Duration
	if d.IdleTimeout != "" {
		var err error
		idleTimeout, err = time.ParseDuration(d.IdleTimeout)
		if err != nil {
			log.Fatalf("[CONFIG]: idle_timeout inválido en '%s': %v\n", endpoint, err)
		}
	}
 
 	// Bloqueamos el acceso al mapa de datos para poder escribir sin errores de acceso
	ar.mu.Lock()
	defer ar.mu.Unlock()
	ar.routes[endpoint] = &RouteActivity{
		lastSeen:    time.Now(),
		state:       StateRunning,
		idleTimeout: idleTimeout,
		persist:     d.Persist,
		route:       r,
	}
}

// [+] Actualiza el timestamp de última actividad de un endpoint
func (ar *ActivityRegistry) Touch(endpoint string) {
	// Comprobamos que exista el registro en memoria haciendo bloqueo de lectura
	ar.mu.RLock()
	ra, ok := ar.routes[endpoint]
	ar.mu.RUnlock()
	if !ok { return }
 
 	// Bloqueo de escritura + actualización del timestamp de acceso
	ra.mu.Lock()
	ra.lastSeen = time.Now()
	ra.mu.Unlock()
}

// [+] Devuelve la RouteActivity de un endpoint para que el handler HTTP pueda comprobar su estado y hacer wake-on-demand si está parado
func (ar *ActivityRegistry) Get(endpoint string) (*RouteActivity, bool) {
	ar.mu.RLock()
	defer ar.mu.RUnlock()
	ra, ok := ar.routes[endpoint]
	return ra, ok
}


// ==========================================
//      VIGILANTE DE INACTIVIDAD (IDLE)
// ==========================================

// [+] Comprobante de los servicios inactivos
// Arranca en background una goroutine que revisa cada minuto si algún contenedor ha superado su idle_timeout
func startIdleWatcher(registry *ActivityRegistry, dockerConfigs map[string]*DockerConfig, dm *DockerManager) {
	ticker := time.NewTicker(1 * time.Minute)

	go func() {
		for range ticker.C {
			// Hacemos snapshot del mapa para liberar el lock del registry lo antes posible
			registry.mu.RLock()
			snapshot := make(map[string]*RouteActivity, len(registry.routes))
			for k, v := range registry.routes { snapshot[k] = v }
			registry.mu.RUnlock()

			for endpoint, ra := range snapshot {
				ra.mu.Lock()

				// Condiciones para no actuar sobre este endpoint
				// - persist: nunca se apaga
				// - idleTimeout == 0: no tiene timeout configurado
				// - state != Running: ya está parado o arrancando (wake-on-demand en curso)
				if ra.persist || ra.idleTimeout == 0 || ra.state != StateRunning {
					ra.mu.Unlock()
					continue
				}

				inactiveDuration := time.Since(ra.lastSeen)

				if inactiveDuration >= ra.idleTimeout {
					// Marcamos como parado ANTES de soltar el lock y llamar a StopService
					// Así ningún handler HTTP puede intentar proxear tráfico a un contenedor que estamos a punto de parar
					ra.state = StateStopped
					ra.mu.Unlock()

					d := dockerConfigs[endpoint]
					if d == nil { continue }

					if err := dm.StopService(endpoint, d); err != nil {
						log.Printf("[IDLE]: Error al apagar '%s': %v\n", endpoint, err)
						// Si el stop falló, revertimos el estado para que el watcher lo reintente en el siguiente tick
						ra.mu.Lock()
						ra.state = StateRunning
						ra.mu.Unlock()
					} else {
						log.Printf("[IDLE]: '%s' apagado tras %.0f min de inactividad\n",
							endpoint, inactiveDuration.Minutes())
					}
				} else {
					// Aún no ha superado el timeout: logueamos cuánto queda
					remaining := ra.idleTimeout - inactiveDuration
					log.Printf("[IDLE]: '%s' activo — se apagará en %.0f min\n",
						endpoint, remaining.Minutes())
					ra.mu.Unlock()
				}
			}
		}
	}()
}



// ====================================================
//      FUNCIONALIDADES PRINCIPALES DEL PROGRAMA
// ====================================================

// Ayuda de la ejecución del servidor
func HelpPannel() {
	log.Fatalf("[USAGE]: %s <config_file>\n", os.Args[0])
}

// Separación de los endpoints
func separeRouteEndpoints(endpoints []Route) (map[string]*httputil.ReverseProxy, map[string]Target, []bool, error) {
	// Creamos las estructuras para identificar:
	// Servicios HTTP y servidios TCP genéricos
	httpRoutes := map[string]*httputil.ReverseProxy{}
	tcpRoutes := map[string]Target{}
	http_s := []bool{false, false}

	used_ports := map[int]bool{}

	// Separación entre conexiones HTTP/HTTPS y TCP genéricas
	for _, r := range endpoints {
		proto := strings.ToLower(r.Target.Protocol)
		switch proto {
			// Protocolo HTTP/HTTPS
			case "http", "https":
				// Identificamos si hay endpoints http / https
				if proto[len(proto)-1] == 's' { http_s[1] = true } else { http_s[0] = true }
				targetURL, _ := url.Parse(fmt.Sprintf("%s://%s:%d", r.Target.Protocol, r.Target.Ip, r.Target.Port))
				httpRoutes[r.Endpoint] = httputil.NewSingleHostReverseProxy(targetURL)
				log.Printf("Ruta %s registrada: %s → %s\n", strings.ToUpper(proto), r.Endpoint, targetURL.String())

			// Protocolos TCP genéricos
			default:
				// [!] Comprobación de que no haya otros proocolos a lanzarse en el mismo puerto
				if used_ports[r.Target.Standar] {
					return nil, nil, nil, fmt.Errorf("[ERROR]: Puerto TCP duplicado %d\n", r.Target.Standar)
				}

				tcpRoutes[r.Endpoint] = r.Target
				used_ports[r.Target.Standar] = true
				log.Printf("Ruta %s registrada: %s → %s:%d\n", strings.ToUpper(proto), r.Endpoint, r.Target.Ip, r.Target.Port)
		}
	}

	return httpRoutes, tcpRoutes, http_s, nil
}

// Redireccionador para HTTP
func httpTcpServiceLauch(httpRoutes map[string]*httputil.ReverseProxy, registry *ActivityRegistry, dockerConfigs map[string]*DockerConfig, routes map[string]Route, dm *DockerManager, wg *sync.WaitGroup) {
	// Marcamos la finalización de la gorutine
	defer wg.Done()
	log.Printf("Proxy HTTP lanzado en el puerto 80...\n")

	err := http.ListenAndServe(":80", http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		host := strings.Split(r.Host, ":")[0]
		
		// Si el dominio se encuentra registrado en el proxy
		// redirecciona al servidor
		proxy, ok := httpRoutes[host]
		if !ok {
			http.Error(w, "Host no encontrado", http.StatusNotFound)
			return
		}

		// ── Wake-on-demand ──────────────────────────────────
		if ra, exists := registry.Get(host); exists {
			ra.mu.Lock()

			if ra.state == StateStopped {
				ra.state = StateStarting
				ra.mu.Unlock()

				log.Printf("[HTTP]: '%s' parado, reactivando servicio...\n", host)

				d := dockerConfigs[host]
				route := routes[host]

				if err := dm.LaunchService(host, d); err != nil {
					log.Printf("[ERROR]: No se pudo reactivar '%s': %v\n", host, err)
					http.Error(w, "Servicio no disponible temporalmente", http.StatusServiceUnavailable)

					ra.mu.Lock()
					ra.state = StateStopped
					ra.mu.Unlock()
					return
				}

				if err := dm.WaitForTarget(host, route.Target, 60*time.Second); err != nil {
					log.Printf("[ERROR]: Timeout reactivando '%s' %vn", host, err)
					http.Error(w, "Servicio tardó demasiado en arrancar", http.StatusGatewayTimeout)

					ra.mu.Lock()
					ra.state = StateStopped
					ra.mu.Unlock()
					return
				}

				ra.mu.Lock()
				ra.state = StateRunning
				ra.mu.Unlock()
			
			} else {
				ra.mu.Unlock()
			}
		}
		// ────────────────────────────────────────────────────

		registry.Touch(host)
		log.Printf("[HTTP]: %s → %s\n", host, r.URL.Path)
		proxy.ServeHTTP(w, r)
	}))

	if err != nil {
		log.Fatal("[ERROR]: Error en el servidor HTTP: %v", err)
	}
}

// Servicio TCP a redireccionar
func genericTcpServiceLaunch(host string, target Target, wg *sync.WaitGroup) {
	// Marcamos la finalización de la gorutine
	defer wg.Done()

	// Lanzamos el servidor que se va a poner en escucha en el puerto por defecto
	listener, err := net.Listen("tcp", fmt.Sprintf(":%d", target.Standar))
	if err != nil {
		log.Fatalf("[ERROR]: Error al escuchar en '%s': %v\n", host, err)
	}
	log.Printf("Proxy %s lanzado en el puerto %d...\n", strings.ToUpper(target.Protocol), target.Standar)
	defer listener.Close()

	// Escuchamos las peticiones que haya a este servidor y se retornan al canal
	for {
		conn, err := listener.Accept()
		if err != nil {
			log.Printf("[ERROR]: Error aceptando conexión TCP: %v", err)
			continue
		}

		// Consumimos las conexiones en el hilo principal del programa
		// y lanzamos las gorutines para tramitar las peticiones TCP
		go HandleTCPConnection(conn, host, target)
	}
}

// Petición de un cliente TCP
func HandleTCPConnection(client net.Conn, host string, target Target) {
	// Cerramos la conexión en caso de que haya algún problema
	// en el tratamiento de los datos
	defer client.Close()

	backend, err := net.Dial("tcp", fmt.Sprintf("%s:%d", target.Ip, target.Port))
	if err != nil {
		log.Printf("[ERROR]: No se pudo conectar al destino %s:%d: %v\n", target.Ip, target.Port, err)
		return
	}
	defer backend.Close()

	log.Printf("[TCP]: Redirigiendo tráfico %s → %s:%d\n", client.RemoteAddr(), target.Ip, target.Port)

	// Bidireccional
	go io.Copy(backend, client)
	io.Copy(client, backend)
}



// ============================
//      PROGRAMA PRINCIPAL
// ============================

// Programa principal
func main() {
	// Parámetros de entrada
	if len(os.Args) < 2 { HelpPannel() }

	// Lectura del archivo de configuración
	config, err := ReadConfigFile(os.Args[1])
	if err != nil { panic(err) }

	// Lectura del archivo de endpoints
	endpoints, err := ReadRoutingFile(config.Endpoints)
	if err != nil { panic(err) }

	// Inicializamos el cliente docker SDK
	dm, err := NewDockerManager()
	if err != nil { log.Fatalf("[DOCKER]: `No se pudo conectar al daemon: %v\n", err) }
	defer dm.Close()

	// Mapas auxiliares para acceso rápido por endpoint
	dockerConfigs := map[string]*DockerConfig{}
	routesByHost  := map[string]Route{}
	for _, r := range endpoints {
		dockerConfigs[r.Endpoint] = r.Docker
		routesByHost[r.Endpoint] = r
	}

	// Lanzamos servicios Docker y registramos actividad
	registry := NewActivityRegistry()
	for _, r := range endpoints {
		if r.Docker == nil { continue }

		if err := dm.LaunchService(r.Endpoint, r.Docker); err != nil {
			log.Fatalf("[DOCKER]: Fallo al lanzar '%s': %v\n", r.Endpoint, err)
		}
		if err := dm.WaitForTarget(r.Endpoint, r.Target, 60*time.Second); err != nil {
			log.Fatalf("%v\n", err)
		}

		registry.Register(r.Endpoint, r.Docker, r)
	}

	startIdleWatcher(registry, dockerConfigs, dm)

	print("\n")

	// Creamos las estructuras para identificar:
	// Servicios HTTP y servidios TCP genéricos
	httpRoutes, tcpRoutes, http_s, err := separeRouteEndpoints(endpoints)
	if err != nil {log.Fatal(err) }

	// [*] Si no hay endpoints, no malgastamos recursos en un servicio
	if len(httpRoutes) + len(tcpRoutes) == 0 {
		log.Printf("No hay ningún servicio configurado...\n")
		os.Exit(0)
	}

	print("\n")

	var wg sync.WaitGroup

	// Lanzamos el servidor para protocolos HTTP
	if http_s[0] {
		wg.Add(1)
		go httpTcpServiceLauch(httpRoutes, registry, dockerConfigs, routesByHost, dm, &wg)
	}

	// Lanzamos el servidor para protocolos HTTPS
	if http_s[1] {
		fmt.Printf("[DEBUG]: Servidor HTTPS no disponible aún...\n")
	}

	// Lanzamos los servidores TCP Genéricos
	for host, target := range tcpRoutes {
		wg.Add(1)
		go genericTcpServiceLaunch(host, target, &wg)
	}

	// ── Capturador de señal de apagado ───────────────────
	sigChan := make(chan os.Signal, 1)
	signal.Notify(sigChan, syscall.SIGTERM, syscall.SIGINT)

	go func() {
		sig := <-sigChan
		log.Printf("\n[SISTEMA]: Señal '%v' recibida, apagando servicios...\n", sig)

		// Parar todos los contenedores gestionados
		for _, r := range endpoints {
			if r.Docker == nil { continue }
			if err := dm.StopService(r.Endpoint, r.Docker); err != nil {
				log.Printf("[SISTEMA]: Error apagando '%s': %v\n", r.Endpoint, err)
			} else {
				log.Printf("[SISTEMA]: '%s' apagado ✓\n", r.Endpoint)
			}
		}

		dm.Close()
		log.Printf("[SISTEMA]: Apagado completo\n")
		os.Exit(0)
	}()
	// ─────────────────────────────────────────────────────


	// Esperamos a que todas las gorutines terminen
	wg.Wait()
}