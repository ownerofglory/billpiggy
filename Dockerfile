# Build phase
FROM golang:1.24 as build

WORKDIR /app

COPY go.mod go.sum ./
RUN go mod download

COPY *.go ./
COPY Makefile ./
COPY . ./

ENV CGO_ENABLED=0
RUN make build

# Run phase
FROM alpine:latest

WORKDIR /root/

COPY --from=build /app/bin/billpiggy /usr/local/bin/billpiggy
EXPOSE 8000

CMD ["billpiggy"]