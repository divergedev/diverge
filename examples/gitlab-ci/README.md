# GitLab CI/CD Integration for Diverge

This directory contains examples of how to integrate Diverge into your GitLab CI/CD pipelines.
Using Diverge in CI allows you to spin up preview environments automatically for each Merge Request, run tests against them, and tear them down when the MR is closed.

## Examples

- **Single Service**: `.gitlab-ci.yml` demonstrates a basic pipeline that builds a Docker image, creates a preview environment, runs a test using the preview header, and cleans up.
- **Multi-Service with PreviewGroups**: The `multi-service/` directory and `.diverge.yaml` show how to deploy multiple interdependent services at once using a `PreviewGroup`.

## Quick Start

1. Add a `.diverge.yaml` file to the root of your repository to define your services and routing configuration.
2. Ensure that your CI pipeline has access to a Kubernetes cluster (e.g., via the GitLab Kubernetes Agent or by providing a `KUBECONFIG` variable).
3. Copy the relevant `.gitlab-ci.yml` structure into your repository.
4. Open a Merge Request to see Diverge build and deploy a preview environment for your branch.

For more details, see the official [Diverge Documentation](https://divergedev.github.io).
