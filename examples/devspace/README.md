# DevSpace Example

This directory contains a minimal `devspace.yaml` showing how to combine `diverge dev` and DevSpace.

## Usage

1. Start the DevSpace pipeline:
   ```bash
   DIVERGE_SERVICE=my-service devspace run diverge-dev
   ```

This will run `diverge dev`, which intercepts `my-service` and then starts `devspace dev` to sync files and start the terminal.

## Remote Development

For full in-cluster remote development, see the [DevSpace Integration Guide](../../docs/guides/devspace-integration.md).
