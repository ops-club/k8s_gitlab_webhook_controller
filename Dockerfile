# Dockerfile for building a Go application with multi-architecture support

# Step 1: Build the Go binary in a multi-architecture base image
FROM --platform=${BUILDPLATFORM} golang:1.23 AS builder

# Set the working directory inside the container
WORKDIR /app

# Copy go.mod and go.sum to the workspace
# COPY go.mod ./

# Download Go modules
# RUN go mod download

# Copy the rest of the application source code
COPY *.go .

# remove any previously initialized go.mod and go.sum files
# (this is in case the container data wasn't destroyed)
RUN rm -f go.mod rm go.sum

# initialize Go modules
RUN go mod init app

# fetch dependencies
RUN go mod tidy

# Set target platform for build (linux/amd64, linux/arm64, etc.)
ARG TARGETOS
ARG TARGETARCH

# Build the Go application targeting the desired platform
RUN CGO_ENABLED=0 GOOS=$TARGETOS GOARCH=$TARGETARCH go build -o k8s-custom-controller main.go

# Step 2: Use a minimal base image for the final container
FROM --platform=${BUILDPLATFORM} alpine:3.20

# Set working directory inside the container
WORKDIR /root/

# Copy the binary from the builder image
COPY --from=builder /app/k8s-custom-controller .

# Define the entry point
CMD ["./k8s-custom-controller"]
