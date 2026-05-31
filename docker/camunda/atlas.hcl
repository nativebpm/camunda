variable "db_url" {
  type    = string
  default = "postgres://camunda:camunda@localhost:7477/process-engine?sslmode=disable"
}

env "local" {
  url = var.db_url
  migration {
    dir = "file://migrations"
  }
}
