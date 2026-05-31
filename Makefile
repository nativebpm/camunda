sequin:
	curl -L https://github.com/sequinstream/sequin/releases/latest/download/sequin-docker-compose.zip -o sequin-docker-compose.zip \
	&& unzip sequin-docker-compose.zip -d docker && rm sequin-docker-compose.zip
	
camunda:
	cd docker/camunda && docker compose up -d