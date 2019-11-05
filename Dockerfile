FROM golang:1.11

WORKDIR /app

COPY . .

RUN go install -v ./cmd/...

EXPOSE 80
ENTRYPOINT ["courier"]
