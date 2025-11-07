# RevPiProx

Reverse Proxy For Raspberry Projects


## Introducción:

Servidor de Proxy Inverso que redirecciona peticiones de servidores establecidos en los puertos "conocidos" por defecto a unos establecidos por el usuario.

Utilidad diseñada para poder tener varios servicios que atentan contra el mismo puerto por defecto, para poder hacer redireccion no solo por IP sono que también por Puerto.

Ejemplo:
- **example1.local** direcciona a `192.168.1.123:80`
- **example2.local** direcciona a `192.168.1.123:80`
Dos servicios HTTP que por defecto se lanzarían en el puerto 80, pero en la máquina están instalados en los puertos **5050** y **5051**. Este proxy puede hacer esa redirección de puertos.


## Dependencias:

- Go
- Makefile


## Instanación:

> [!IMPORTANT]
> Ejecutar como el usuario `root`

```bash
make install
```


## Desinstalación:

> [!IMPORTANT]
> Ejecutar como el usuario `root`

```bash
sudo make uninstall
```


## Archivos de configuración:

> [!NOTE]
> Los archivos de configuración por defecto se encuentra en la ruta `/etc/rpp`.

Archivo `config.yml`:

```yml
# Nombre del servidor
name: RevPiProx

# Version actual del proyecto (dejar por defecto)
version: 1.0

# Puerto en el que se lanzará el servidor
port: 80

# Protocolo por el cual se entablarán las conexiones
protocol: tcp

# Condpoints configurados para poder hacer saolución por puerto
endpoints: /etc/rpp/endpoints.json
```

Archivo `endpoints.json`:

```json
[
	{
		"endpoint": "ssh.local",
		"target": {
			"protocol": "ssh",
			"ip":       "192.168.1.123",
			"port":     5050,
			"standar":  22
		}
	},
	{
		"endpoint": "http1.local",
		"target": {
			"protocol": "http",
			"ip":       "192.168.1.123",
			"port":     5051,
			"standar":  80
		}
	},
	{
		"endpoint": "http2.local",
		"target": {
			"protocol": "http",
			"ip":       "192.168.1.123",
			"port":     5052,
			"standar":  80
		}
	},
	{
		"endpoint": "http3.local",
		"target": {
			"protocol": "http",
			"ip":       "192.168.1.321",
			"port":     5050,
			"standar":  80
		}
	}
]
```