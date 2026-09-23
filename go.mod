module github.com/egori/facebook-aggregator

go 1.25.0

toolchain go1.25.13

require (
	github.com/jackc/pgx/v5 v5.9.2
	github.com/joho/godotenv v1.5.1
	github.com/teslashibe/facebook-go v0.0.0
	golang.org/x/text v0.39.0
)

require (
	github.com/jackc/pgpassfile v1.0.0 // indirect
	github.com/jackc/pgservicefile v0.0.0-20240606120523-5a60cdf6a761 // indirect
	github.com/jackc/puddle/v2 v2.2.2 // indirect
	golang.org/x/sync v0.21.0 // indirect
)

replace github.com/teslashibe/facebook-go => ./third_party/facebook-go
