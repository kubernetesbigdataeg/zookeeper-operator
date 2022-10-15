# Get zookeeper pods
kubectl get pods -l app=zk

# The StatefulSet controller provides each Pod with a unique hostname based 
# on its ordinal index. The hostnames take the form of <statefulset name>-<ordinal index>. 
# Because the replicas field of the zk StatefulSet is set to 3, the Set's 
# controller creates three Pods with their hostnames set to zk-0, zk-1, and zk-2.
for i in 0 1 2; do kubectl exec zk-$i -- hostname; done

# The servers in a ZooKeeper ensemble use natural numbers as unique identifiers,
# and store each server's identifier in a file called myid in the server's data directory.
for i in 0 1 2; do echo "myid zk-$i";kubectl exec zk-$i -- cat /var/lib/zookeeper/data/myid; done

# To get the Fully Qualified Domain Name (FQDN) of each Pod in the zk StatefulSet 
# use the following command. The zk-hs Service creates a domain for all of the 
# Pods, zk-hs.default.svc.cluster.local.
for i in 0 1 2; do kubectl exec zk-$i -- hostname -f; done

# ZooKeeper stores its application configuration in a file named zoo.cfg. 
# Use kubectl exec to view the contents of the zoo.cfg file in the zk-0 Pod.
kubectl exec zk-0 -- cat /opt/zookeeper/conf/zoo.cfg

# Check log it's in console output
kubectl exec zk-0 cat /usr/etc/zookeeper/log4j.properties

kubectl exec zk-0 -- zkCli.sh create /hello world
kubectl exec zk-1 -- zkCli.sh ls /
kubectl exec zk-2 -- zkCli.sh get /hello
kubectl exec zk-1 -- zkCli.sh delete /hello

# k exec -ti zk-0 -- bash
# k logs zk-0 --tail 20
# k logs zk-0 -f

