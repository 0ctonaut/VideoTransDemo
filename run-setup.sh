#!/bin/bash
# 包装脚本，自动输入 sudo 密码
export SUDO_ASKPASS=/bin/echo
echo "1" | sudo -S zsh setup.zsh 2>&1

