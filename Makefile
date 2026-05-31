sequin:
	curl -L https://github.com/sequinstream/sequin/releases/latest/download/sequin-docker-compose.zip -o sequin-docker-compose.zip \
	&& unzip sequin-docker-compose.zip -d docker && rm sequin-docker-compose.zip
	
camunda:
	cd docker/camunda && docker compose up -d

atlas-hash:
	docker run --rm -v $(PWD):/camunda -w /camunda/docker/camunda arigaio/atlas:latest migrate hash

atlas-status:
	docker run --rm --network camunda_default \
		-v $(PWD)/docker/camunda/migrations:/migrations \
		arigaio/atlas:latest-alpine migrate status \
		--url "postgres://camunda:camunda@camunda_postgres:5432/process-engine?sslmode=disable" \
		--dir "file:///migrations"

atlas-apply:
	docker run --rm --network camunda_default \
		-v $(PWD)/docker/camunda/migrations:/migrations \
		arigaio/atlas:latest-alpine migrate apply \
		--url "postgres://camunda:camunda@camunda_postgres:5432/process-engine?sslmode=disable" \
		--dir "file:///migrations" --allow-dirty