package main

/**
 * Author: ApardoO
 * */

import (
	"gopkg.in/yaml.v3"
	
	"net/http/httputil"
	"encoding/json"
	"net/http"
	"net/url"
	"strings"
	"sync"
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

// >>> Archivo de endpoints <<<
type Target struct {
	Protocol string `json:"protocol"` // Protocolo
	Ip       string `json:"ip"`       // Dirección IP a la que resuelve
	Port     int    `json:"port"`     // Puerto en el que se ha configurado el servicio
	Standar  int    `json:"standar"`  // Puerto estándar en el que normalmente resuelve el servicio
}

type Route struct {
	Endpoint string `json:"endpoint"` // Nombre host a redireccionar
	Target   Target `json:"target"`   // Configuración del endpoint a redirigir
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
					return nil, nil, nil, fmt.Errorf("[ERROR]: Puerto TCP duplicado %d...\n", r.Target.Standar)
				}

				tcpRoutes[r.Endpoint] = r.Target
				used_ports[r.Target.Standar] = true
				log.Printf("Ruta %s registrada: %s → %s:%d\n", strings.ToUpper(proto), r.Endpoint, r.Target.Ip, r.Target.Port)
		}
	}

	return httpRoutes, tcpRoutes, http_s, nil
}

// Redireccionador para HTTP
func httpTcpServiceLauch(httpRoutes map[string]*httputil.ReverseProxy, wg *sync.WaitGroup) {
	// Marcamos la finalización de la gorutine
	defer wg.Done()
	log.Printf("Proxy HTTP lanzado en el puerto 80...\n")
	err := http.ListenAndServe(":80", http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		host := strings.Split(r.Host, ":")[0]
		
		// Si el dominio se encuentra registrado en el proxy
		// redirecciona al servidor
		if proxy, ok := httpRoutes[host]; ok {
			log.Printf("[HTTP]: %s → %s\n", host, r.URL.Path)
			proxy.ServeHTTP(w, r)
			return
		}

		// En caso de que no se encuentre, devolvemos un
		// código de salida erróneo
		http.Error(w, "Host no encontrado", http.StatusNotFound)
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

	// Creamos las estructuras para identificar:
	// Servicios HTTP y servidios TCP genéricos
	httpRoutes, tcpRoutes, http_s, err := separeRouteEndpoints(endpoints)
	if err != nil {log.Fatal(err) }

	// [*] Si no hay endpoints, no malgastamos recursos en un servicio
	if len(httpRoutes) + len(tcpRoutes) == 0 {
		log.Printf("No hay ningún servicio configurado...")
		os.Exit(0)
	}

	print("\n")

	var wg sync.WaitGroup

	// Lanzamos el servidor para protocolos HTTP
	if http_s[0] {
		wg.Add(1)
		go httpTcpServiceLauch(httpRoutes, &wg)
	}

	// Lanzamos el servidor para protocolos HTTPS
	if http_s[1] {
		fmt.Printf("[DEBUG]: Servidor HTTPS no disponible...")
	}

	// Lanzamos los servidores TCP Genéricos
	for host, target := range tcpRoutes {
		wg.Add(1)
		go genericTcpServiceLaunch(host, target, &wg)
	}

	// Esperamos a que todas las gorutines terminen
	wg.Wait()
}