variable "url" {
  type    = string
  default = getenv("DATABASE_URL")
}

env "preview" {
  url = var.url
  migration {
    dir = "file://migrations"
  }
  lint {
    destructive {
      error = true
    }
  }
}
