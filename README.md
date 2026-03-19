# Azure ApplicationHealth Extension for Linux (V2)
[![Build Status](https://travis-ci.org/Azure/applicationhealth-extension-linux.svg?branch=master)](https://travis-ci.org/Azure/applicationhealth-extension-linux)

[![GitHub Build Status](https://github.com/Azure/applicationhealth-extension-linux/actions/workflows/go.yml/badge.svg)](https://github.com/Azure/applicationhealth-extension-linux/actions/workflows/go.yml)

The application health extension periodically probes for application health within a Linux VM when configured.
The result of the health checks guide automatic actions that can take place on VMs such as stopping rolling upgrades
across a set of VMs and repairing VMs as they become unhealthy.

## Testing

### Running Unit Tests Locally

The extension is built for Linux. To compile and run tests on a non-Linux machine, set the appropriate environment variables:

```bash
# Build
GOOS=linux GOARCH=amd64 CGO_ENABLED=0 go build -o bin/applicationhealth-extension ./main

# Compile tests (Linux binary - must be run on Linux or in a container)
GOOS=linux GOARCH=amd64 CGO_ENABLED=0 go test -c -o test_binary ./main/
```

To run tests inside the dev container:

```bash
make devcontainer
# Inside the container:
go test -v ./main/
```

### Idempotency Behavior

The enable command is idempotent: if a healthy AHE process is already running with the same
sequence number, a new enable invocation will detect it and exit gracefully instead of
restarting the process. This prevents unnecessary downtime during rolling upgrades.

Health is determined by checking the handler.log file freshness. A heartbeat is logged every 5 minutes.
If the log file hasn't been updated within 10 minutes, the existing process is considered
unresponsive and the new process will take over.

-----
This project has adopted the [Microsoft Open Source Code of Conduct](https://opensource.microsoft.com/codeofconduct/). For more information see the [Code of Conduct FAQ](https://opensource.microsoft.com/codeofconduct/faq/) or contact [opencode@microsoft.com](mailto:opencode@microsoft.com) with any additional questions or comments.
