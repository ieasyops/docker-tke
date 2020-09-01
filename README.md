## 简述

腾讯tke本地开发环境搭建
基于docker swarm 构建，方便调试


## 步骤

1. 本地安装docker,docker-compose,
2. 执行 docker swarm init
3. 查看当前节点名称 
```
docker node ls
ID                            HOSTNAME            STATUS              AVAILABILITY        MANAGER STATUS      ENGINE VERSION
yov8y4dr81gthcr7za2m245o9 *   docker-desktop      Ready               Active              Leader              19.03.8
```

4. 为当前节点添加tag

```
docker node update --label-add=tke.base=true node
```
5. 创建 overlay 网路, create network

```

docker network create -d overlay traefik-net

```

6. 启动 traefik

```

cd traefik && ./run.sh

```

7. 启动 tke

cd ..
bash start.sh  