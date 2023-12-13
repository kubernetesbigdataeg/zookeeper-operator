podman build . --tag docker.io/kubernetesbigdataeg/zookeeper:3.9.1-1
podman login docker.io -u kubernetesbigdataeg
podman push docker.io/kubernetesbigdataeg/zookeeper:3.9.1-1
