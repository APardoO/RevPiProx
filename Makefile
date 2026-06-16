# Variables generales:
SCRIPT_NAME = ./deploy.sh

.PHONY: all install uninstall help



# Regas principales
all: help

# Instalación completa
install:
	chmod +x $(SCRIPT_NAME)
	@$(SCRIPT_NAME) -c -i

# Modifica las flags para que no se lance el servidor
# al instalarlo
quiet-install:
	chmod +x $(SCRIPT_NAME)
	@$(SCRIPT_NAME) -q -c -i

# Desinstalación
uninstall:
	chmod +x $(SCRIPT_NAME)
	@$(SCRIPT_NAME) -u

# Ayuda por pantalla
help:
	chmod +x $(SCRIPT_NAME)
	@$(SCRIPT_NAME) -h