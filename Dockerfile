FROM gcr.io/distroless/static-debian12:nonroot
ARG TARGETPLATFORM
COPY ${TARGETPLATFORM}/hclapi /hclapi
USER nonroot:nonroot
ENTRYPOINT ["/hclapi"]