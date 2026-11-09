module github.com/weaveplatform/infraweave

go 1.27.3

require github.com/jackc/pgx/v5 v5.0.0

replace github.com/jackc/pgx/v5 => ./stub/pgx
