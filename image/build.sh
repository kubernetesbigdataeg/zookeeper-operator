podman build . --tag docker.io/kubernetesbigdataeg/zookeeper:3.7.0-1
podman login docker.io -u kubernetesbigdataeg
podman push docker.io/kubernetesbigdataeg/zookeeper:3.7.0-1
