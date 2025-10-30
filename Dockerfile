FROM golang:1.24

WORKDIR /workspace
COPY . .

RUN apt-get update && apt-get install -y bash

# Install Kubernetes code generators
RUN for gen in client-gen lister-gen informer-gen deepcopy-gen; do \
      go install k8s.io/code-generator/cmd/$gen@v0.31.0 ; \
    done

ENTRYPOINT ["bash"]
