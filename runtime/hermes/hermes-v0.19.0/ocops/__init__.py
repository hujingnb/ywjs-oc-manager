"""oc-ops：把 hermes 镜像内 oc-* 运维脚本逻辑下沉为可 import 的核心模块，
供 server.py 的 HTTP handler 与各 oc-* CLI shim 共用。"""
