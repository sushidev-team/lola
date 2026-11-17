# syntax=docker/dockerfile:1.7
#
# The build context is the REPOSITORY ROOT, because the hub builds against the
# checkout's ../hubproto:
#
#   docker build -f hub/Dockerfile -t lola-hub .
#
# hub/Dockerfile.dockerignore keeps the context to hub/ and hubproto/.
FROM golang:1.25 AS build
WORKDIR /src
# The hub and hubproto are stdlib only: nothing is downloaded.
ENV GOPROXY=off GOFLAGS=-buildvcs=false CGO_ENABLED=0
COPY hubproto ./hubproto
COPY hub ./hub
WORKDIR /src/hub
RUN go build -trimpath -ldflags="-s -w" -o /out/lola-hub ./cmd/lola-hub

FROM gcr.io/distroless/static-debian12:nonroot
COPY --from=build /out/lola-hub /lola-hub
USER nonroot:nonroot
# Mount a volume here; the store snapshot is /data/hub.json (0600).
VOLUME ["/data"]
ENV LOLA_HUB_DATA=/data/hub.json
EXPOSE 8080
ENTRYPOINT ["/lola-hub"]
CMD ["serve", "--addr", ":8080"]
