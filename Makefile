# https://github.com/camunda/docker-camunda-bpm-platform
camunda:
	docker run -d --name camunda -p 8080:8080 camunda/camunda-bpm-platform:latest

camunda-env:
	docker run -d --name camunda -p 8080:8080 --link postgresql:db \
			--env-file camunda.env camunda/camunda-bpm-platform:latest

camunda-start:
	docker start camunda

camunda-stop:
	docker stop camunda
