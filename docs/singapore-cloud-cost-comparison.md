# 新加坡云资源组合价格核验报告

> 核验日期：2026-07-20  
> 计费口径：按需、730 小时/月、无促销与预留折扣、未含税。  
> 目标组合：Kubernetes（4 个 8c16 Linux 工作节点）、MySQL 高可用读写双节点（8c16）、Redis 8GB、Elasticsearch 8c16、10Mbps 固定公网带宽。
> 为使数据库报价可复现，MySQL 统一按 200GB 存储；ES 在厂商允许指定时按 100GB 存储。各厂商产品的最低拓扑可能与目标不同，已在规格列注明。

## 结论摘要

本报告已将 MySQL 固定为 200GB、ES 固定为 100GB，并给出可计月费排序；但 AWS、Google Cloud 和 Azure 的公网出网按实际流量计费，不存在与“10Mbps 固定带宽包月”一一对应的产品，华为云又缺少三个托管实例的实时价，因此这不是最终合同总价排名。

若 10Mbps 固定带宽是刚性条件，应优先比较腾讯云国际、阿里云国际和华为云国际。改为 4 个 8c16 节点后，三者的工作节点价均需在官方登录计算器读取；华为云国际的 RDS、DCS、CSS 实例也仍需在官方计算器/购买页取得实时价。

## 按当前配置的每月花费摘要

下表使用本报告已固定的配置：4 个 8c16 工作节点、MySQL HA + 200GB、Redis 高可用、ES 8c16（能配置时为 100GB）、1 个公网 IP/10Mbps。金额为 USD/月。

| 排序 | 厂商 | 每月花费 | 结论 |
| ---: | --- | ---: | --- |
| 1 | Google Cloud | **2,778.02 + 公网出网** | 使用 4 个 Arm `c4a-highcpu-8` 节点与 Elastic Cloud；若 GKE 管理费抵扣适用，为 **2,705.02 + 公网出网**。 |
| 2 | Azure | **3,795.70 + 公网出网** | 4 个 `Standard_F8s_v2` 节点；MySQL 最近规格为 8c32，Redis 为约 9.6GB 可用，ES 为 Elastic Cloud。 |
| — | AWS | **2,240.82 + 4 × c6i.2xlarge 实时报价 + EBS/公网出网** | 已计 EKS、MySQL、Redis、OpenSearch、IPv4；8c16 节点新加坡价格需在 AWS Calculator/Offer Index 复核。 |
| — | 腾讯云国际 | **1,050.24 + 4 × SA5.2XLARGE16 实时报价** | 已计 TKE L5、MySQL、Redis、ES、10Mbps；8c16 节点价需登录 CVM 计算器获取。 |
| — | 阿里云国际 | **1,438.71 + 4 × ecs.u1-c1m2.2xlarge 实时报价** | 已计 ACK Pro、MySQL、Redis、ES、10Mbps；8c16 节点价需登录 ECS 购买页获取。 |
| — | 华为云国际 | **201.60 + 4 × c6.2xlarge.2 实时报价 + RDS/DCS/CSS 实例询价** | 已计 CCE HA、MySQL 200GB 存储与 10Mbps；工作节点和关键托管实例需登录计算器获取。 |

> AWS、Google Cloud、Azure 的“公网出网”按实际 GB/GiB 和目的地收费，不是固定 10Mbps 月费，因此上述金额不能与固定带宽套餐做完全等价比较。腾讯云、阿里云、华为云和 AWS 的 8c16 工作节点仍待登录计算器或 Offer Index 返回，不能据当前已计部分进行跨厂商排序。

## 重要可比性约束

- Kubernetes 按 4 个 8c16 Linux 工作节点比较；控制面、系统盘、负载均衡等可能另计。
- MySQL 高可用计算费之外，存储、备份、I/O 或网络费可能另计。
- Redis 仅写“8GB”不足以确定价格；高可用主从通常至少需要两个节点。
- ES 的“8c16”仅可视为单节点规格；生产高可用通常还需要多个数据节点、专用主节点和存储。
- 固定带宽是带宽上限；按量出网是实际传输量。10Mbps 满载运行一个月约产生 3.24–3.29TB 出网，不能直接等同为固定带宽费用。

## 已核验的月度价格

以下表格按服务横向排列。`—` 表示未取得可公开核验的同口径价格，并非免费。

### Kubernetes

| 厂商 | 价格（USD/月） | 规格 |
| --- | ---: | --- |
| 腾讯云国际 | 14.90 + 4 × 实时报价 | 标准 TKE L5 控制面 14.90 USD + 4 个 `SA5.2XLARGE16` Linux 8c16 工作节点；每节点 50GB 通用型 SSD 系统盘。当前匿名计算器未返回该 SKU 的区域价格，需登录 CVM 计算器。 |
| 阿里云国际 | 65.70 + 4 × 实时报价 | ACK Pro 控制面 65.70 USD + 4 个 `ecs.u1-c1m2.2xlarge` Linux 8c16 工作节点；每节点 40GiB ESSD AutoPL 系统盘。需登录 ECS 购买页取实时价。 |
| AWS | 73.00 + 4 × 实时报价 | EKS 标准支持 73.00 USD + 4 个 `c6i.2xlarge` Linux 8c16 工作节点；EBS、IPv4 另计。该节点的官方新加坡价格需在 AWS Calculator/Offer Index 复核。 |
| Google Cloud | 957.88 | GKE 管理费 73.00 USD + 4 个 Arm `c4a-highcpu-8` 8c16 工作节点，每节点 221.22 USD/月。一个 Autopilot 集群或单个单可用区 Standard 集群可适用 74.40 USD/月管理费抵扣；该节点为 Arm，x86 需另行报价。 |
| Azure | 1,217.64 | AKS Standard 控制面 73.00 USD + 4 个 Standard_F8s_v2 Linux 8c16 工作节点，每节点 286.16 USD/月；磁盘另计。 |
| 华为云国际 | 58.40 + 4 × 实时报价 | CCE HA 控制面 58.40 USD + 4 个 `c6.2xlarge.2` Linux 8c16 工作节点；需登录官方计算器取实时价。 |

### MySQL

| 厂商 | 价格（USD/月） | 规格 |
| --- | ---: | --- |
| 腾讯云国际 | 526.29 | 8c16、双节点、200GB；官方实时计算器，其中计算规格为 491 USD。 |
| 阿里云国际 | 547.63 | MySQL 8.0、x86、高可用主备、8c16 独享型、High-performance cloud disk、200GB、包月 1 个月。Premium ESSD 同配置为 593.84 USD/月。 |
| AWS | 1,423.22 | 标准两节点 Multi-AZ 最近规格 `db.m7g.2xlarge`，8c32 + GP3 200GB；含主备计算与双份存储。严格 8c16 仅有三实例 readable standby，价格为 1,815.07 USD/月。 |
| Google Cloud | 907.79 | Cloud SQL MySQL Enterprise、8c16、Regional HA、200GiB SSD；HA 的计算与存储均按双份计，不含备份和网络。 |
| Azure | 1,409.04 | MySQL Flexible Server General Purpose、最近规格 8c32、HA、200GB ZRS；主备计算与双份存储均计费。 |
| 华为云国际 | 29.20 + 实例询价 | `rds.mysql.n1.2xlarge.2.ha`（8c16 主备）+ 200GB 存储；当前公开 AP-Singapore 存储费为 0.0002 USD/GB·小时，即 29.20 USD/月，实例计算费仅在官方计算器/购买页返回。 |

### Redis

| 厂商 | 价格（USD/月） | 规格 |
| --- | ---: | --- |
| 腾讯云国际 | 114.62 | Redis 7.0、标准架构、8GB、1 主 1 副本、包年包月 1 个月；官方实时计算器价。 |
| 阿里云国际 | 198.56 | Redis OSS 5.0、经典部署、标准主从、8GB、2 节点、按量付费：0.272 USD/小时。包月 1 个月同配置为 130.56 USD。 |
| AWS | 360.62 | ElastiCache r6g.large，13.07GiB、主从两节点；180.31 USD/节点/月，无已核验的恰好 8GiB 节点。 |
| Google Cloud | 438.00 | Memorystore for Redis Standard Tier、8GiB；自带跨区复制与自动故障切换，不启用额外可读副本。 |
| Azure | 570.86 | Azure Managed Redis B10 HA 双节点；12GB 总内存、约 9.6GB 可用，不是严格 8GB。 |
| 华为云国际 | 106.85（官方示例）+ 实时询价 | DCS 文档的 8GB master/standby、2 replicas 官方计费示例；示例不是当前 AP-Singapore 实时 SKU 报价，实际价格需在 DCS 计算器返回。 |

### Elasticsearch / OpenSearch

| 厂商 | 价格（USD/月） | 规格 |
| --- | ---: | --- |
| 腾讯云国际 | 318.28 | ES 8c16、Basic 单节点 + 100GB Premium 存储；实例费 310.98 USD，存储费 0.0001 USD/GB·小时 × 100GB × 730 小时 = 7.30 USD。Platinum 实例费为 346.02 USD/月。 |
| 阿里云国际 | 550.67 | 向量增强版 ES 8.17.0、单 AZ、Turbo-1 8c16 数据节点 ×1、100GB ESSD PL1；含默认 Kibana 1c2g。标准版计算器的最小前台配置为两台 8c16、20GiB/节点，价格为 1,026.38 USD/月。 |
| AWS | 380.33 | OpenSearch c6g.2xlarge.search、8c16 单节点；不含 EBS、快照、专用主节点。 |
| Google Cloud | 470.70 | Elastic Cloud Hosted，新加坡 16GB 数据热节点（8c16 级别）×1；含该 SKU 随 RAM 配置的计算与磁盘，不含快照、流量、Kibana/主节点。 |
| Azure | 594.51 | Elastic Cloud Hosted，新加坡 16GB 数据热节点（8c16 级别）×1；含该 SKU 随 RAM 配置的计算与磁盘，不含快照、流量、Kibana/主节点。 |
| 华为云国际 | 622.08（2025 官方方案示例）+ 实时询价 | 官方方案示例为 3 × 4c8、100GB/节点 CSS，总价 0.86 USD/小时；不是目标 8c16 SKU，当前 CSS 实时价格需计算器返回。 |

### 公网 IP / 带宽

| 厂商 | 价格（USD/月） | 规格 |
| --- | ---: | --- |
| 腾讯云国际 | 76.15 | EIP、常规 BGP、固定 10Mbps、包月。 |
| 阿里云国际 | 76.15 | EIP、BGP、固定 10Mbps、包月。 |
| AWS | 3.65 + 按量出网 | 1 个公网 IPv4；公网出网按 GB 另计，不是固定 10Mbps 带宽。 |
| Google Cloud | 3.65 + 按量出网 | 1 个在用外部 IPv4；公网出网按 GiB 另计，不是固定 10Mbps 带宽。 |
| Azure | 3.65 + 按量出网 | 1 个标准公网 IPv4；公网出网按 GB 另计，不是固定 10Mbps 带宽。 |
| 华为云国际 | 114.00 | 独享 Dynamic BGP、固定 10Mbps、包年包月；按需价为 0.25 USD/小时，即 182.50 USD/月。 |

### 可计合计

下表只相加已取得价格，**不是全栈总价排名**。存在 `缺项`、`非同配` 或 `非实时参考` 时，不能据此判定厂商更便宜。

| 厂商 | 已计价格（USD/月） | 覆盖项 | 不能用于最终比价的原因 |
| --- | ---: | --- | --- |
| 腾讯云国际 | 1,050.24 + 4 × SA5.2XLARGE16 实时报价 | TKE L5、MySQL、Redis、ES 100GB、固定 10Mbps | 8c16 节点新加坡价需登录 CVM 计算器获取。 |
| 阿里云国际 | 1,438.71 + 4 × ecs.u1-c1m2.2xlarge 实时报价 | ACK Pro、MySQL、Redis 按量、ES、固定 10Mbps | 8c16 节点新加坡价需登录 ECS 购买页获取；ES 为向量增强版。 |
| AWS | 2,240.82 + 4 × c6i.2xlarge 实时报价 + 出网/EBS/存储 | EKS、MySQL、Redis、OpenSearch、IPv4 | 8c16 节点新加坡价待 AWS Calculator/Offer Index 复核；MySQL 为 8c32，Redis 为 13.07GiB，OpenSearch 未含 EBS。 |
| Google Cloud | 2,778.02 + 出网 | GKE + 4 × Arm 8c16、MySQL、Redis、Elastic、IPv4 | 若 GKE 管理费抵扣适用则为 2,705.02 USD；节点为 Arm，Elastic 非 Google 第一方，公网不是固定 10Mbps。 |
| Azure | 3,795.70 + 出网 | AKS + 4 × 8c16、MySQL、Redis、Elastic、IPv4 | MySQL 为 8c32，Redis 为约 9.6GB 可用，Elastic 非 Azure 第一方，公网不是固定 10Mbps。 |
| 华为云国际 | 201.60 + 4 × c6.2xlarge.2 实时报价 + RDS/DCS/CSS 实例询价 | CCE HA、MySQL 200GB 存储、固定 10Mbps | 8c16 节点与 RDS/DCS/CSS 的实例实时价不公开；Redis 与 CSS 已列官方示例价，但未纳入合计。 |

## 厂商逐项判断

### 腾讯云国际

腾讯云国际已核验除工作节点外的固定部分为 **1,050.24 USD/月**。新口径为“标准 TKE L5 + 4 个 `SA5.2XLARGE16` 8c16 CVM + MySQL 200GB + Redis 8GB 主从 + ES Basic 单节点 100GB + 10Mbps”，最终总价需叠加 4 个工作节点的登录计算器实时报价。

### 阿里云国际与阿里云国内版

阿里云国际的 ACK、ECS、EIP、RDS MySQL 高可用、Redis 主从及 ES 均已在新加坡官方匿名计算器取价。RDS 的磁盘类型、Redis 的按量/包月方式，以及 ES 版本与节点数会显著影响总价，表中已保留实际选择。

阿里云中国站与国际站的账户、结算和合规体系独立；中国站可采购全球区域资源，但不应将中国大陆区域的 CNY 价格当成新加坡价格。

### AWS

AWS 的公开 Offer Index 可以核验 EKS、ElastiCache、OpenSearch 与 IPv4 的 SKU 价格。请求的 MySQL “8c16、双节点”没有完全等价的已核验价格：可匹配的 `db.c6gd.2xlarge` 出现在 Multi-AZ readable standby（三实例）架构中，不能伪装为两节点报价。公网访问使用按 GB 出网，不提供 10Mbps 固定公网带宽包。

### Google Cloud

GKE、Cloud SQL MySQL 和 Memorystore Redis 均可提供接近的服务能力；Cloud SQL 可支持 8c16 高可用，Memorystore Standard Tier 有跨区复制和自动故障转移。但 Google Cloud 没有第一方托管 Elasticsearch/OpenSearch，需单独采用 Marketplace 方案；公网出网按 GB 计费，没有固定 10Mbps 包月项。

### Azure

Azure AKS、MySQL、Managed Redis 均可用，但 MySQL 通用型 8 核最低为 32GiB，因此无法严格满足 8c16，必须上配。ES 需通过 Elastic Cloud 的 Azure Marketplace 集成单独配置；公网出网按 GB 计费。

### 华为云国际

华为云 AP-Singapore 的 CCE、ECS、10Mbps EIP 与 RDS 存储费已核验；RDS 8c16 主备、DCS 8GB 主备和 CSS 8c16 的实例实时价仍只在官方计算器/购买页显示。表中保留了官方文档示例价，但没有把示例当作实时总价。

## 建议的采购顺序

1. 固定 10Mbps 是刚性条件：先比较腾讯云国际、阿里云国际、华为云国际。
2. 可以接受按量公网出网：再将 AWS、Google Cloud、Azure 按实际月出网量纳入总价模型。
3. 先固定以下参数，再请求所有厂商同口径报价：MySQL 磁盘与备份容量、Redis 主从/集群架构、ES 数据节点数与磁盘、Kubernetes 系统盘及控制面 SLA、月度出网量和主要目的地。

## 官方来源与登录报价入口

### 腾讯云

- [MySQL 实时计算器](https://intl.cloud.tencent.com/pricing/cdb/calculator)
- [Redis 计费规则](https://www-sg.tencentcloud.com/document/product/239/31954)
- [Redis 计算器](https://buy.intl.cloud.tencent.com/spu-price/redis/calculator)
- [标准 TKE 计费](https://intl.cloud.tencent.com/document/product/457/45157)
- [CVM 8c16 新加坡计算器](https://intl.cloud.tencent.com/pricing/cvm/calculator)
- [ES 价目表](https://www.tencentcloud.com/document/product/845/18376)
- [EIP 固定带宽](https://intl.cloud.tencent.com/document/product/213/39743)

### 阿里云

- [ACK Pro 计费](https://www.alibabacloud.com/help/en/ack/ack-managed-and-ack-dedicated/product-overview/ack-pro-cluster-billing)
- [新加坡 ECS 8c16 购买页](https://ecs-buy.alibabacloud.com/ecs#/custom/prepay/ap-southeast-1)
- [EIP 包月带宽](https://www.alibabacloud.com/help/en/eip/subscription)
- [RDS MySQL 新加坡计算器](https://www.alibabacloud.com/zh/pricing-calculator#/commodity/rds_intl)
- [Redis 新加坡按量计算器](https://www.alibabacloud.com/en/pricing-calculator?_p_lc=1&regionId=ap-southeast-1#/commodity/kvstore_intl)
- [Elasticsearch 新加坡计算器](https://www.alibabacloud.com/zh/pricing-calculator#/commodity/elasticsearchpre_intl)
- [数据库产品报价入口](https://www.alibabacloud.com/help/en/apsaradb/yaochi-database-product-pricing-summary)
- [中国站与国际站差异](https://www.alibabacloud.com/help/en/account/aliyun-vs-alibaba-cloud)

### AWS

- [EKS 定价](https://aws.amazon.com/eks/pricing/)
- [ElastiCache 定价](https://aws.amazon.com/elasticache/pricing/)
- [RDS MySQL 定价](https://aws.amazon.com/rds/mysql/pricing/)
- [公网 IPv4 定价](https://aws.amazon.com/vpc/pricing/)
- [AWS Pricing Calculator](https://calculator.aws/)
- [RDS MySQL 新加坡 Offer Index](https://pricing.us-east-1.amazonaws.com/offers/v1.0/aws/AmazonRDS/current/ap-southeast-1/index.json)

### Google Cloud

- [GKE 定价](https://cloud.google.com/kubernetes-engine/pricing)
- [Compute Engine 通用机型定价](https://cloud.google.com/products/compute/pricing/general-purpose)
- [Cloud SQL 定价](https://cloud.google.com/sql/pricing)
- [Memorystore 定价](https://cloud.google.com/memorystore/docs/redis/pricing)
- [Elastic Cloud Hosted 新加坡价格](https://cloud.elastic.co/deployment-pricing-table?productType=stack_hosted&provider=gcp&region=gcp-asia-southeast1)
- [VPC 网络出网定价](https://cloud.google.com/vpc/network-pricing)
- [Google Cloud 计算器](https://cloud.google.com/products/calculator)

### Azure

- [Azure Retail Prices API（新加坡）](https://prices.azure.com/api/retail/prices?%24filter=armRegionName%20eq%20%27southeastasia%27)
- [AKS 定价](https://azure.microsoft.com/en-us/pricing/details/kubernetes-service/)
- [AKS Free / Standard 定价层](https://learn.microsoft.com/en-us/azure/aks/free-standard-pricing-tiers)
- [MySQL 高可用计费规则](https://learn.microsoft.com/en-us/azure/mysql/flexible-server/concepts-high-availability-faq)
- [Elastic Cloud Hosted 新加坡价格](https://cloud.elastic.co/deployment-pricing-table?productType=stack_hosted&provider=azure&region=azure-southeastasia)
- [Azure Pricing Calculator](https://azure.microsoft.com/pricing/calculator/)

### 华为云

- [价格计算器](https://www.huaweicloud.com/intl/en-us/pricing/calculator.html)
- [CCE 支持区域](https://support.huaweicloud.com/intl/en-us/productdesc-cce/cce_productdesc_0014.html)
- [CCE HA 与 AP-Singapore 方案价格](https://support.huaweicloud.com/intl/en-us/appcc-ctf/ctf-appcc%284%29.pdf)
- [RDS MySQL 规格](https://support.huaweicloud.com/intl/en-us/productdesc-rds-mysql/rds-mysql-productdesc-pdf.pdf)
- [AP-Singapore 10Mbps EIP 价格](https://support.huaweicloud.com/intl/en-us/amisvcd-aislt/amisvcd_02.html)
- [RDS MySQL 计费与存储费](https://support.huaweicloud.com/intl/en-us/price-rds-mysql/rds_00_0006.html)
- [DCS 计费示例](https://support.huaweicloud.com/intl/en-us/price-dcs/dcs_04_0004.html)
