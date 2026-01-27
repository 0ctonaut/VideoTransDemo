#!/bin/zsh
# 清理旧的命名空间（避免冲突）
sudo ip netns del server 2>/dev/null
sudo ip netns del client 2>/dev/null

# 清理旧的 veth 设备
sudo ip link del vsrv 2>/dev/null
sudo ip link del vcli 2>/dev/null
sudo ip link del veth-server 2>/dev/null
sudo ip link del veth-client 2>/dev/null

# 1. 创建server和client命名空间
sudo ip netns add server
sudo ip netns add client

# 2. 创建虚拟网卡对（用于 server 和 client 之间通信）
sudo ip link add veth-server type veth peer name veth-client

# 3. 将网卡分别加入两个命名空间
sudo ip link set veth-server netns server
sudo ip link set veth-client netns client

# 4. 配置IP地址（同一网段，用于 server 和 client 之间通信）
sudo ip netns exec server ip addr add 192.168.100.1/24 dev veth-server
sudo ip netns exec client ip addr add 192.168.100.2/24 dev veth-client

# 5. 启用网卡和回环接口
sudo ip netns exec server ip link set veth-server up
sudo ip netns exec server ip link set lo up
sudo ip netns exec client ip link set veth-client up
sudo ip netns exec client ip link set lo up

# 6. 模拟网络瓶颈（可选：配置延迟/带宽/丢包）
sudo ip netns exec client tc qdisc add dev veth-client root netem delay 50ms rate 10mbit loss 0.5%

# ========== 新增：创建连接到主机的 veth 对（用于访问外网）==========
# 为 server 创建连接到主机的 veth 对（使用短名称避免超过15字符限制）
sudo ip link add vsrv type veth peer name vsrv-gw
sudo ip link set vsrv-gw netns server
sudo ip link set vsrv up

# 为 server 配置网关 IP
sudo ip netns exec server ip addr add 192.168.200.2/24 dev vsrv-gw
sudo ip addr add 192.168.200.1/24 dev vsrv
sudo ip netns exec server ip link set vsrv-gw up

# 为 client 创建连接到主机的 veth 对
sudo ip link add vcli type veth peer name vcli-gw
sudo ip link set vcli-gw netns client
sudo ip link set vcli up

# 为 client 配置网关 IP
sudo ip netns exec client ip addr add 192.168.201.2/24 dev vcli-gw
sudo ip addr add 192.168.201.1/24 dev vcli
sudo ip netns exec client ip link set vcli-gw up

# ========== 配置DNS ==========
# 使用主机的 DNS 配置，如果主机没有则使用备用 DNS
HOST_DNS=$(grep nameserver /etc/resolv.conf | head -1 | awk '{print $2}' 2>/dev/null)
if [ -z "$HOST_DNS" ]; then
    HOST_DNS="114.114.114.114"
fi

# 配置多个 DNS 服务器（优先使用 8.8.8.8，因为它更可靠）
sudo ip netns exec server bash -c "echo -e 'nameserver 8.8.8.8\nnameserver 8.8.4.4\nnameserver $HOST_DNS' > /etc/resolv.conf"
sudo ip netns exec client bash -c "echo -e 'nameserver 8.8.8.8\nnameserver 8.8.4.4\nnameserver $HOST_DNS' > /etc/resolv.conf"

# 添加常用域名的 /etc/hosts 条目（绕过 DNS 解析问题）
# 注意：不要将 proxy.golang.org 指向 goproxy.cn 的 IP，因为 SSL 证书不匹配
# 只添加 goproxy.cn 到 /etc/hosts，让 Go 通过 GOPROXY 环境变量使用它
GOPROXY_IP=$(getent hosts goproxy.cn 2>/dev/null | awk '{print $1}' | head -1)
if [ -n "$GOPROXY_IP" ]; then
    sudo ip netns exec server bash -c "echo '$GOPROXY_IP goproxy.cn' >> /etc/hosts"
    sudo ip netns exec client bash -c "echo '$GOPROXY_IP goproxy.cn' >> /etc/hosts"
    echo "已添加 goproxy.cn 到 /etc/hosts: $GOPROXY_IP"
fi

# 添加主机名到 /etc/hosts（解决 sudo 警告）
HOSTNAME=$(hostname 2>/dev/null || echo "localhost")
HOST_IP=$(hostname -I 2>/dev/null | awk '{print $1}' || echo "127.0.0.1")
if [ -n "$HOSTNAME" ] && [ "$HOSTNAME" != "localhost" ]; then
    sudo ip netns exec server bash -c "echo '$HOST_IP $HOSTNAME' >> /etc/hosts"
    sudo ip netns exec client bash -c "echo '$HOST_IP $HOSTNAME' >> /etc/hosts"
    echo "已添加主机名 $HOSTNAME 到 /etc/hosts: $HOST_IP"
fi

# ========== 配置外网路由 + NAT转发（核心！）==========
# 步骤A：给主机启用IP转发（让主机作为网关）
sudo sysctl -w net.ipv4.ip_forward=1 > /dev/null

# 步骤B：获取主机的外网网卡（通常是eth0/ens33/wlan0，替换为你的实际网卡名）
# 先自动检测外网网卡（优先选有默认路由的网卡，选择 metric 最小的）
HOST_IF=$(ip route | grep default | sort -k5 | head -1 | awk '{print $5}')
if [ -z "$HOST_IF" ]; then
    HOST_IF="eth0"  # 自动检测失败时，手动指定（根据你的机器调整）
fi

# 验证网卡是否存在
if ! ip link show $HOST_IF >/dev/null 2>&1; then
    echo "警告：检测到的网卡 $HOST_IF 不存在，使用 eth0"
    HOST_IF="eth0"
fi

# 步骤C：配置正确的默认路由（通过主机的 veth 设备）
sudo ip netns exec server ip route add default via 192.168.200.1 dev vsrv-gw
sudo ip netns exec client ip route add default via 192.168.201.1 dev vcli-gw

# 步骤D：配置NAT转发（让命名空间的流量通过主机外网网卡出去）
# 清空旧的 NAT 规则（只清空我们创建的规则，不影响系统其他规则）
sudo iptables -t nat -D POSTROUTING -s 192.168.200.0/24 -o $HOST_IF -j MASQUERADE 2>/dev/null
sudo iptables -t nat -D POSTROUTING -s 192.168.201.0/24 -o $HOST_IF -j MASQUERADE 2>/dev/null

# 添加 NAT 规则（必须放在其他 MASQUERADE 规则之前）
sudo iptables -t nat -I POSTROUTING 1 -s 192.168.200.0/24 -o $HOST_IF -j MASQUERADE
sudo iptables -t nat -I POSTROUTING 1 -s 192.168.201.0/24 -o $HOST_IF -j MASQUERADE

# 允许从 veth 设备到主机外网网卡的转发（清空旧规则）
sudo iptables -D FORWARD -i vsrv -o $HOST_IF -j ACCEPT 2>/dev/null
sudo iptables -D FORWARD -i vcli -o $HOST_IF -j ACCEPT 2>/dev/null
sudo iptables -D FORWARD -i $HOST_IF -o vsrv -j ACCEPT 2>/dev/null
sudo iptables -D FORWARD -i $HOST_IF -o vcli -j ACCEPT 2>/dev/null

# 添加 FORWARD 规则（允许转发）
# 首先允许已建立的连接和相关的连接（确保响应包能返回）
sudo iptables -I FORWARD 1 -m state --state ESTABLISHED,RELATED -j ACCEPT

# 然后允许从 veth 设备到主机外网网卡的转发
sudo iptables -I FORWARD 1 -i vsrv -o $HOST_IF -j ACCEPT
sudo iptables -I FORWARD 1 -i vcli -o $HOST_IF -j ACCEPT

# 允许从主机外网网卡到 veth 设备的转发（响应包）
sudo iptables -I FORWARD 1 -i $HOST_IF -o vsrv -j ACCEPT
sudo iptables -I FORWARD 1 -i $HOST_IF -o vcli -j ACCEPT

# 确保 FORWARD 链的默认策略允许转发（如果被设置为 DROP）
sudo iptables -P FORWARD ACCEPT 2>/dev/null

# ========== 修复 nftables 规则（如果系统使用 nftables）==========
# 清理并重新配置 nftables FORWARD 规则（确保规则顺序正确）
if command -v nft >/dev/null 2>&1; then
    # 清理 FORWARD 链
    sudo nft flush chain ip filter FORWARD 2>/dev/null
    
    # 添加正确的规则（ESTABLISHED,RELATED 必须在最前面）
    sudo nft add rule ip filter FORWARD ct state established,related accept 2>/dev/null
    sudo nft add rule ip filter FORWARD iifname "vsrv" oifname "eth0" accept 2>/dev/null
    sudo nft add rule ip filter FORWARD iifname "eth0" oifname "vsrv" accept 2>/dev/null
    sudo nft add rule ip filter FORWARD iifname "vcli" oifname "eth0" accept 2>/dev/null
    sudo nft add rule ip filter FORWARD iifname "eth0" oifname "vcli" accept 2>/dev/null
    
    # 清理并重新配置 NAT POSTROUTING 规则
    sudo nft flush chain ip nat POSTROUTING 2>/dev/null
    sudo nft add rule ip nat POSTROUTING ip saddr 192.168.200.0/24 oifname "eth0" masquerade 2>/dev/null
    sudo nft add rule ip nat POSTROUTING ip saddr 192.168.201.0/24 oifname "eth0" masquerade 2>/dev/null
    
    echo "已修复 nftables 规则"
fi

# 注意：server 和 client 之间的通信通过 veth-server/veth-client 直接连接，不需要 iptables 规则

# ========== 调试信息 ==========
echo ""
echo "=== 网络配置调试信息 ==="
echo "检测到的主机外网网卡：$HOST_IF"
echo ""
echo "Server namespace 路由："
sudo ip netns exec server ip route 2>/dev/null || echo "  无法查看（需要 sudo）"
echo ""
echo "Server namespace IP 配置："
sudo ip netns exec server ip addr show 2>/dev/null | grep -E "inet |^[0-9]+:" || echo "  无法查看（需要 sudo）"
echo ""
echo "主机上的 veth 设备："
ip link show | grep -E "vsrv|vcli" || echo "  未找到"
echo ""
echo "iptables NAT 规则："
sudo iptables -t nat -L POSTROUTING -n -v 2>/dev/null | grep -E "192.168.200|192.168.201" || echo "  无法查看（需要 sudo）"
echo ""
echo "iptables FORWARD 规则："
sudo iptables -L FORWARD -n -v 2>/dev/null | grep -E "vsrv|vcli" || echo "  无法查看（需要 sudo）"
echo "========================"
echo ""

echo "网络环境创建完成："
echo "server命名空间 IP：192.168.100.1（与client通信）"
echo "server命名空间网关：192.168.200.2（访问外网）"
echo "client命名空间 IP：192.168.100.2（与server通信）"
echo "client命名空间网关：192.168.201.2（访问外网）"
echo "网络瓶颈：50ms延迟 + 10Mbps带宽 + 0.5%丢包（仅限client）"
echo "外网配置：已启用IP转发+NAT，命名空间可访问公网"
echo "主机外网网卡：$HOST_IF"
echo ""
echo ""
echo "提示：在 network namespace 中运行 Go 命令时，请使用："
echo "  sudo ip netns exec server env GOPROXY=https://goproxy.cn,direct go run main.go -offer"
echo ""
echo "测试网络连接（按顺序测试）："
echo "  1. 测试到网关：sudo ip netns exec server ping -c 2 192.168.200.1"
echo "  2. 测试到主机：sudo ip netns exec server ping -c 2 192.168.58.46"
echo "  3. 测试到外网：sudo ip netns exec server ping -c 3 8.8.8.8"
echo "  4. 测试 DNS：sudo ip netns exec server nslookup goproxy.cn"
echo "  5. 测试 HTTPS：sudo ip netns exec server curl -I https://goproxy.cn"

# ========== 配置 Go 环境变量（使用国内镜像）==========
# 在 network namespace 中设置环境变量，配置 GOPROXY 使用国内镜像
sudo ip netns exec server bash -c 'echo "export GOPROXY=https://goproxy.cn,direct" >> /etc/profile'
sudo ip netns exec server bash -c 'echo "export GOSUMDB=sum.golang.google.cn" >> /etc/profile'
sudo ip netns exec client bash -c 'echo "export GOPROXY=https://goproxy.cn,direct" >> /etc/profile'
sudo ip netns exec client bash -c 'echo "export GOSUMDB=sum.golang.google.cn" >> /etc/profile'