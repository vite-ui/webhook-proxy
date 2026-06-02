FROM golang:1.22-alpine AS build
WORKDIR /app
COPY main.go .
RUN CGO_ENABLED=0 go build -o relay-server main.go

FROM alpine:3.19
COPY --from=build /app/relay-server /relay-server
EXPOSE 8080
CMD ["/relay-server"]
