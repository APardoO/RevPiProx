#!/bin/bash

# =====================
#  >> Author: ApardoO
# =====================

# >>> Variables globales ==================================

# Nombre del programa
declare -r tool_name="RevPiProx"
declare -r program_name="rpp"

# Colours
declare -r greenColour="\e[0;32m\033[1m"
declare -r endColour="\033[0m\e[0m"
declare -r redColour="\e[0;31m\033[1m"
declare -r blueColour="\e[0;34m\033[1m"
declare -r yellowColour="\e[0;33m\033[1m"
declare -r purpleColour="\e[0;35m\033[1m"
declare -r turquoiseColour="\e[0;36m\033[1m"
declare -r grayColour="\e[0;37m\033[1m"


# >>> Métodos del programa ================================

# Panel de ayuda
function helpPannel() {
	echo -e "[USAGE]: $0 [-c] [-i] [-u] [-h] [-q]"
	tput cnorm
	exit 1
}

# Compilación
function compile() {
	echo -e "${blueColour}[BUILD]${endColour} ${grayColour}Compilando${endColour} ${purpleColour}$program_name${endColour}${grayColour}...${endColour}"
	go build -o ./"$program_name" ./*.go

	if [ $? -eq 0 ]; then
		echo -e "\n${blueColour}[${endColour}${greenColour}OK${endColour}${blueColour}]${endColour} ${grayColour}Compilación exitosa:${endColour} ${yellowColour}./$program_name${endColour}"
	else
		echo -e "\n${redColour}[ERROR]${endColour} ${yellowColour}Falló la compilación${yellowColour}"
		tput cnorm
		exit 1
	fi
}

# Instalación
function install() {
	global q_flag

	# Comprobación del UUID actual
	if [ "$(id -u)" != "0" ]; then
		echo -e "\n${redColour}[!]${endColour} ${yellowColour}No tienes permisos suficientes...${endColour}"
		tput cnorm
		exit 1
	fi

	echo -e "${yellowColour}[*]${endColour} ${grayColour}Instalando $program_name...${endColour}"

	# Creación de los directorios apropiados
	mkdir -p /etc/"$program_name"
	mkdir -p /var/lib/"$program_name"

	# Copia de los archivos de configuración (/etc/rpp) y de servicio (/etc/systemd/system) más el ejecutable (/usr/local/bin)
	cp -v ./config.yml /etc/"$program_name"/ || { tput cnorm; exit 1; }
	cp -v ./endpoints.json /etc/"$program_name"/ || { tput cnorm; exit 1; }
	cp -v ./"$program_name" /usr/local/bin/ || { tput cnorm; exit 1; }
	cp -v ./"$program_name.service" /etc/systemd/system/ || { tput cnorm; exit 1; }

	# Creación de un usuario para la ejecución del servicio siendo NO root
	if ! id "$program_name" &>/dev/null; then
		useradd -r -s /usr/sbin/nologin "$program_name"

		# Comprobar que docker existe
		if [ -n "$(grep '^docker:' /etc/group)" ]; then
			# Agregar al usuario al grupo docker
			sudo usermod -aG docker "$program_name"
		fi
	else
		echo -e "${yellowColour}[+]${endColour} ${grayColour}El usuario${endColour} ${purpleColour}$program_name${endColour} ${grayColour}ya existe, omitiendo creación...${endpoints}"
	fi

	# Asignación de permisos
	chmod +x /usr/local/bin/"$program_name"
	chown "$program_name:$program_name" /usr/local/bin/"$program_name"
	chown root:root /etc/systemd/system/"$program_name.service"
	chmod 644 /etc/systemd/system/"$program_name.service"
	chown -R "$program_name:$program_name" /etc/"$program_name"
	chmod 755 /etc/"$program_name"
	
	chown "$program_name:$program_name" /var/lib/"$program_name"

	# Recargamos el demonio de systemd
	systemctl daemon-reload

	# Lanzamos el servicio
	if [[ $q_flag == 0 ]] && systemctl is-active --quiet "$program_name.service"; then
		systemctl restart "$program_name.service"
	else
		systemctl start "$program_name.service"
	fi

	# Habilitamos el servicio para que se ejecute al arrancar
	systemctl enable "$program_name.service"

	echo -e "${greenColour}[OK]${endColour} ${grayColour}Instalación completada!${endColour}"
	systemctl --no-pager status "$program_name.service" --lines=3
}

# Desinstalación
function uninstall() {
	# Comprobación del UUID actual
	if [ "$(id -u)" != "0" ]; then
		echo -e "\n${redColour}[!]${endColour} ${yellowColour}No tienes permisos suficientes...${endColour}"
		tput cnorm
		exit 1
	fi

	echo -e "${yellowColour}[*]${endColour} ${grayColour}Desinstalando $program_name...${endColour}"

	# Confirmación de desinstalación
	read -rp "${grayColour}¿Seguro que quieres desinstalar $program_name? [y/N]: ${endColour}" confirm
	if [[ ! "$confirm" =~ ^[yY]$ ]]; then
		echo -e "[*] Cancelando desinstalación..."
		return
	fi

	# Detener el servicio
	if systemctl is-active --quiet "$program_name.service"; then
		systemctl stop "$program_name.service"
	fi

	# Deshabilitar del arranque el servicio
	if systemctl is-enabled --quiet "$program_name.service"; then
		systemctl disable "$program_name.service"
	fi

	# Eliminar el archivo del servicio
	if [ -f "/etc/systemd/system/$program_name.service" ]; then
		rm -v "/etc/systemd/system/$program_name.service"
	fi

	# Eliminar el ejecutable
	if [ -f "/usr/local/bin/$program_name" ]; then
		rm -v "/usr/local/bin/$program_name"
	fi

	# Eliminar los archivos de configuración
	if [ -d "/etc/$program_name" ]; then
		rm -rv "/etc/$program_name"
	fi

	# Recargar systemd
	systemctl daemon-reload

	# Eliminación de posibles riesgos de fallos previos del servicio
	systemctl reset-failed "$program_name.service" &>/dev/null

	# Eliminación del usuario rpp
	if id "$program_name" &>/dev/null; then
		# Verificar que no haya procesos activos de este usuario
		if pgrep -u "$program_name" &>/dev/null; then
			echo -e "${redColour}[ERROR]${redColour} ${yellowColour}No se puede eliminar al usuario $program_name...${yellowColour}"
		else
			echo -e "${blueColour}[+]${endColour} ${grayColour}Eliminando al usuario${endColour} ${purpleColour}$program_name${endColour}${grayColour}...${endColour}"
			userdel -r "$program_name" &>/dev/null || echo -e "${redColour}[ERROR]${endColour} ${yellowColour}No se puede eliminar al usuario $program_name...${endColour}"
			groupdel "$program_name" &>/dev/null || true
		fi
	fi

	echo -e "${greenColour}[OK]${endColour} ${grayColour}Desinstalación completada!${endColour}"
}


# >>> Programa Principal ==================================

tput civis
set -e
declare -i q_flag=0; declare -i i_flag=0; declare -i u_flag=0; while getopts "qciuh" arg; do
	case $arg in
		q) let q_flag += 1 ;;
		c) compile;;
		i) install;;
		u) uninstall;;
		h) helpPannel;;
		*) helpPannel;;
	esac
done

# En caso de no pasar argumentos
if [ $OPTIND -eq 1 ]; then
	helpPannel
fi
tput cnorm