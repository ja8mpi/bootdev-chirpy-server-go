module github.com/ja8mpi/bootdev-chirpy-server-go

go 1.24.4

require (
	github.com/golang-jwt/jwt/v5 v5.2.3
	github.com/google/uuid v1.6.0
	github.com/joho/godotenv v1.5.1
	github.com/lib/pq v1.10.9
)

require (
	github.com/ja8mpi/go-auth v0.0.0-20250719054447-0b71b69f2d14 // indirect
	golang.org/x/crypto v0.40.0 // indirect
)

replace (
	github.com/ja8mpi/go-auth v0.0.0-20250719054447-0b71b69f2d14 => ./internal/go-auth // indirect
)
