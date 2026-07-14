# syntax=docker/dockerfile:1

FROM golang:1.22-alpine AS build
WORKDIR /src
RUN apk add --no-cache ca-certificates
COPY go.mod go.sum ./
RUN go mod download
COPY . .
RUN CGO_ENABLED=0 GOOS=linux go build -ldflags="-s -w" -o /out/mysql-housekeeper ./cmd/mysql-housekeeper

FROM gcr.io/distroless/static-debian12:nonroot
COPY --from=build /out/mysql-housekeeper /mysql-housekeeper
ENTRYPOINT ["/mysql-housekeeper"]
