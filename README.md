# Google Cloud Build Command Wrapper

This is a thin command wrapper for use inside a Google Cloud Build step. 

## Use Case

This was written with a very narrow use case in mind: wrapping Terraform commands.  It performs the following steps:

1. Given a project and build ID, it retrieves the build state from the Cloud Build API
1. It reads the time at which the build will be force-terminated, looks at the `--before-timeout` value (default: 60 seconds), and sets a timer triggering at _build termination time_ minus the _before timeout_ value.
1. Runs the command supplied as its arguments as a child process
1. Waits until the command completes successfully OR...
1. Sends a signal (supplied with `--signal`, by default `SIGTERM`) to the child process when the timer triggers, allowing the process to gracefully terminate ahead of the Cloud Build force-termination

### But Why?

Google Cloud Builds have a timeout value, which by default is (currently) 10 minutes.  When this timeout for the build (and specifically for the build job itself) is reached, the build service sends a SIGTERM, followed by a very brief pause, and the container in whcih the step is running is force-terminated.  This has serious consequences for a stateful application like Terraform, which often needs to terminate gracefully in order to terminate its operations, update and write state, and to release the state lock. If a job is force-terminated, this cannot happen and can often have dire consequences, like requiring manual, human intervention to recover the state and manually delete the lock.

## Use

**Note:** This has been tested to target Go 1.14 and has not been tested with any version previously released.

 Building for Cloud Build:
 
```
GOOS=linux GOARCH=amd64 build -o gcbcw github.com/angstwad/google-cloud-build-command-wrapper 
```

Cloud Build [steps](https://cloud.google.com/cloud-build/docs/build-config#build_steps) are simply container image tags, so to use it, you'll need to drop the compiled binary inside a container image and push it to a registry of your choice.  Since the canonical use case is in conjunction with Terraform, we'll just need a `Dockerfile` that drops the binary in a [Terraform image](https://hub.docker.com/r/hashicorp/terraform/):

```Dockerfile
FROM hashicorp/terraform:latest

COPY gcbcw /usr/local/bin/gcbcw
```

Build the `Dockerfile`.  In the below example, I'm building with a tag to ship this container image to a [Google Container Registry](https://cloud.google.com/container-registry/) repo in a GCP project under my control.

```bash
docker build -t gcr.io/angstwad-gcbcw/gcbcw:latest

docker push gcr.io/angstwad-gcbcw/gcbcw:latest
```

Last, use the image in your Cloud Build [configuration](https://cloud.google.com/cloud-build/docs/build-config), which is often `cloudbuild.yaml`. An [example](example/cloudbuild.yaml) has been provided.  Because the Terraform image was defined with a custom `entrypoint`, you will need to override this in your build config with the [entrypoint](https://cloud.google.com/cloud-build/docs/build-config#entrypoint) flag.  Below, I have provided an example build step, which wraps the command `terraform apply -auto-approve`, signalling Terraform 2 minutes before the build job's scheduled timeout.

The below example uses [default variable substitutions](https://cloud.google.com/cloud-build/docs/configuring-builds/substitute-variable-values#using_default_substitutions) to populate the required project and build ID parameters.

```yaml
- name: gcr.io/angstwad-gcbcw/gcbcw
  entrypoint: gcbcw
  args: ["--before-timeout", "2m", "$PROJECT_ID", "$BUILD_ID", "--", "terraform", "apply", "-auto-approve"]
```

## Help

A self-documenting `--help` command is available to show flags and parameters.

```
Usage of gcbcw: [flags ...] PROJECT_ID BUILD_ID -- COMMAND [command-flags ...]
      --before-timeout string   time before build timeout to send designated signal ex: 30s, 5m (default "60s")
      --help                    print this usage and exit
      --signal string           signal to send to wrapped process (default "SIGTERM")
```

## Disclaimer

This is not an official Google product.
