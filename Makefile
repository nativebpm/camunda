# https://github.com/camunda/docker-camunda-bpm-platform
camunda:
	docker run -d --name camunda -p 8080:8080 \
			--env-file camunda.env camunda/camunda-bpm-platform:latest

camunda-db:
	docker run -d --name camunda-db -p 8080:8080 --link postgresql:db \
			--env-file camunda.env --env-file camunda.db.env camunda/camunda-bpm-platform:latest
