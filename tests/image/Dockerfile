FROM alpine:3.15.4

ARG ARCH=amd64

ENV ZK_USER=zookeeper \
ZK_DATA_DIR=/var/lib/zookeeper/data \
ZK_DATA_LOG_DIR=/var/lib/zookeeper/log \
ZK_LOG_DIR=/var/log/zookeeper \
JAVA_HOME=/usr/lib/jvm/java-8-openjdk

ARG ZK_VERSION=3.7.0

RUN set -x \
    && apk update \
    && apk add openjdk8-jre \
    && apk add netcat-openbsd \
    && apk add bash 

RUN wget -q "https://archive.apache.org/dist/zookeeper/zookeeper-$ZK_VERSION/apache-zookeeper-$ZK_VERSION-bin.tar.gz" \
    && export GNUPGHOME="$(mktemp -d)" \
    && export ZOO_4LW_COMMANDS_WHITELIST=rouk \
    && tar -xzf "apache-zookeeper-$ZK_VERSION-bin.tar.gz" -C /opt \
    && ln -s /opt/apache-zookeeper-$ZK_VERSION-bin /opt/zookeeper \
    && apk del wget \
    && rm -rf /var/lib/apt/lists/*

RUN wget https://github.com/kubernetes-big-data-eg/propgen/releases/latest/download/propgen \
    && chmod 777 propgen

#Copy configuration generator script to bin
COPY scripts /opt/zookeeper/bin/

# Create a user for the zookeeper process and configure file system ownership 
# for nessecary directories and symlink the distribution as a user executable
RUN set -x \
	&& adduser -D ${ZK_USER} \
	&& [ `id -u $ZK_USER` -eq 1000 ] \
	&& [ `id -g $ZK_USER` -eq 1000 ] \  
    && mkdir -p $ZK_DATA_DIR $ZK_DATA_LOG_DIR $ZK_LOG_DIR /usr/share/zookeeper /tmp/zookeeper /usr/etc/ \
	&& chown -R "$ZK_USER:$ZK_USER" /opt/apache-zookeeper-$ZK_VERSION-bin $ZK_DATA_DIR $ZK_LOG_DIR $ZK_DATA_LOG_DIR /tmp/zookeeper \
	&& ln -s /opt/zookeeper/conf/ /usr/etc/zookeeper \
	&& ln -s /opt/zookeeper/bin/* /usr/bin \
	&& ln -s /opt/zookeeper/lib/* /usr/share/zookeeper 
