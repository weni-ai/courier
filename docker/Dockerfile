FROM golang:1.23.3-alpine3.19 AS builder

WORKDIR /app

RUN apk add --no-cache --virtual build-deps curl gcc libc-dev

COPY go.sum go.mod ./
RUN --mount=type=cache,target=/go/pkg/mod/ \
    go mod download -x

COPY . .
RUN --mount=type=cache,target=/go/pkg/mod/ \
    --mount=type=bind,target=. \
    go install -v ./cmd/...

FROM alpine:3.18.4

RUN apk add --no-cache tzdata

ENV APP_USER=app \
    APP_GROUP=app \
    USER_ID=11999 \
    GROUP_ID=11999

RUN addgroup --system --gid ${GROUP_ID} ${APP_GROUP} \
    && adduser --system --disabled-password --home /home/${APP_USER} \
    --uid ${USER_ID} --ingroup ${APP_GROUP} ${APP_USER}

COPY --from=builder --chown=${APP_USER}:${APP_GROUP} /go/bin/ /app/

WORKDIR /app

USER ${APP_USER}:${APP_GROUP}

EXPOSE 8080
ENTRYPOINT ["./courier"]
