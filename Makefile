# Variables generales:
SCRIPT_NAME = ./deploy.sh

.PHONY: all install uninstall help

INSTALL_FLAGS = -c -i



# Regas principales
all: help

# Instalación completa
install:
	chmod +x $(SCRIPT_NAME)
	@$(SCRIPT_NAME) $(INSTALL_FLAGS)

# Modifica las flags para que no se lance el servidor
# al instalarlo
quiet-install: INSTALL_FLAGS += -q
quiet-install:
	chmod +x $(SCRIPT_NAME)
	@$(SCRIPT_NAME) $(INSTALL_FLAGS)

# Desinstalación
uninstall:
	chmod +x $(SCRIPT_NAME)
	@$(SCRIPT_NAME) -u

# Ayuda por pantalla
help:
	chmod +x $(SCRIPT_NAME)
	@$(SCRIPT_NAME) -h