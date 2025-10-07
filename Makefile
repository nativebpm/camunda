# open browser with url: http://localhost:8080/camunda-welcome/index.html
# auth: demo / demo
camunda:
	docker run -d --name camunda -p 8080:8080 camunda/camunda-bpm-platform:latest

camunda-start:
	docker start camunda

camunda-stop:
	docker stop camunda
