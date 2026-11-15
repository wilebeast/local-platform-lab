# 风控特征工程学习指南：从业务信号到特征平台

> 目标：建立一套从风控业务问题、特征设计、时间窗口计算，到 Registry、Serving、流批一致和生产治理的完整认知。
>
> 适用对象：风控产品、风控策略、数据工程、后端工程、机器学习工程和平台架构人员。

## 1. 为什么要同时学习风控特征和特征平台

风控特征工程包含两个不能割裂的问题：

1. **业务问题**：什么数据能够描述风险，为什么它与风险相关？
2. **工程问题**：怎样稳定、准确、低延迟地计算、复用和读取这些数据？

只研究 Feature Store，容易做出技术平台，却不知道应该生产什么特征；只研究欺诈模型，又容易忽略时间一致性、复用、血缘和线上服务。

完整链路应当是：

```text
业务事实 Fact
    ↓ 清洗、映射、关联
风险特征 Feature
    ↓ Registry、计算、物化、Serving
因子值 Factor Value
    ↓ Rule / Model
风险决策 Decision
    ↓
Action / Review / Investigation
    ↓
结果标签与反馈
```

其中：

- Feature 描述“我们观察到了什么”；
- Factor 定义决策引擎如何取得和使用一个值；
- Rule/Model 判断这些值代表什么风险；
- Feature Platform 不执行普通业务规则；
- 风控结果和后续损失会形成标签，反过来改进 Feature、Rule 和 Model。

## 2. 学习知识地图

### 2.1 风控业务特征

```text
当前事件特征
├── 金额、币种、时间、渠道、国家
├── IP、设备、邮箱、支付工具
└── 当前请求上下文

主体画像特征
├── 账户年龄、认证状态、历史等级
├── 常用设备、常用国家、常用商户
└── 历史成功率、拒绝率、争议率

速度与窗口特征
├── 5m / 1h / 24h / 7d 次数与金额
├── 首次、最近一次、时间间隔
├── 均值、方差、最大值、增长率
└── 入金、出金、授权、退款比例

关系与网络特征
├── 一个设备关联多少账户
├── 一个支付工具关联多少主体
├── 账户、商户、设备之间的图关系
└── 风险节点距离和风险群组密度

标签与反馈特征
├── 欺诈、拒付、退款、损失
├── 人工审核结论
├── 名单与限制历史
└── 标签延迟和标签置信度
```

### 2.2 特征平台工程

```text
Feature Definition
    ↓
Registry 与版本治理
    ↓
Batch / Streaming Computation
    ↓
History + Current Projection
    ↓
Materialization
    ↓
Feature Serving
    ↓
Point-in-time Correctness
    ↓
Freshness / Quality / Lineage / Monitoring
```

## 3. 核心学习资料

### 3.1 风控业务与特征设计

#### 资料 A：Fraud Detection Handbook

- 总入口：[Reproducible Machine Learning for Credit Card Fraud Detection](https://fraud-detection-handbook.github.io/fraud-detection-handbook/README.html)
- 背景：[信用卡欺诈检测系统](https://fraud-detection-handbook.github.io/fraud-detection-handbook/Chapter_2_Background/FDS.html)
- 方法：[欺诈检测中的机器学习](https://fraud-detection-handbook.github.io/fraud-detection-handbook/Chapter_2_Background/MachineLearningForFraudDetection.html)
- 实践：[基础特征变换](https://fraud-detection-handbook.github.io/fraud-detection-handbook/Chapter_3_GettingStarted/BaselineFeatureTransformation.html)

重点学习：

- 交易、账户和终端特征；
- 滑动窗口和历史聚合；
- 类别极不平衡；
- 欺诈标签延迟；
- 按时间划分训练集和验证集；
- Precision、Recall、Top-K 等指标。

建议产物：独立实现一组账户与支付工具的 `1h/24h/7d` 窗口特征，并解释每个特征对应的风险假设。

#### 资料 B：Stripe Radar

- [Stripe Radar Technical Guide](https://stripe.com/guides/primer-on-machine-learning-for-fraud-protection)
- [How we built Stripe Radar](https://stripe.com/blog/how-we-built-it-stripe-radar)
- [Radar 规则与风险属性](https://stripe.com/blog/new-radar-features)

重点学习：

- 当前交易与历史行为如何组合；
- 跨主体、跨商户网络信号；
- 首次出现、最近出现、关联主体数量；
- 规则、模型和人工审核怎样协作；
- 风险拦截与误杀之间的 Precision/Recall 权衡；
- 欺诈模式漂移与持续特征迭代。

建议产物：整理一张“当前事件、主体画像、速度、关系网络、反馈标签”的风险信号矩阵。

#### 资料 C：Amazon Fraud Detector

- [Variables 与变量类型](https://docs.aws.amazon.com/frauddetector/latest/ug/variables.html)
- [Transaction Fraud Insights](https://docs.aws.amazon.com/frauddetector/latest/ug/transaction-fraud-insights.html)
- [Fraud Prediction](https://docs.aws.amazon.com/frauddetector/latest/ug/getting-fraud-predictions.html)

重点学习：

- 事件变量和实体变量；
- 变量类型与默认值；
- IP、邮箱、BIN 等数据增强；
- 实体级和事件级聚合；
- 模型分数、规则和结果的组合。

建议产物：把现有风险输入分成 Value、Feature、RPC、Expression 四类 Factor，并标明每个值的来源和时效。

### 3.2 特征计算与流批一致

#### 资料 D：Airbnb Chronon

- [Chronon GitHub](https://github.com/airbnb/chronon)
- [Airbnb Chronon 介绍](https://airbnb.tech/opensource/chronon/)

重点学习：

- `GroupBy` 与窗口聚合；
- `Join` 与消费视图；
- Batch 和 Streaming 共用定义；
- Point-in-time Correct Backfill；
- Online Serving；
- 在线、离线结果一致性；
- Freshness、Drift 和质量监控。

Chronon 的核心思路可以概括为：

```text
Source
  ↓
GroupBy
  ↓
Window Aggregation
  ↓
Batch / Streaming Materialization
  ↓
Join
  ↓
Online / Offline Consumer
```

建议产物：为同一个 `subject_fund_activity_24h` 分别实现批计算和流计算，并在固定 `as_of` 下比较结果。

#### 资料 E：Feathr

- [Feathr GitHub](https://github.com/feathr-ai/feathr)
- [Feathr 架构介绍](https://feathr-ai.github.io/feathr/talks/Feathr%20Feature%20Store%20Talk.pdf)

重点学习：

- Anchor Feature；
- Derived Feature；
- Lookup Feature；
- 滑动窗口聚合；
- Spark SQL/PySpark 转换；
- 批流统一；
- Point-in-time Join；
- Feature Registry 和复用。

建议产物：画出原始字段、基础 Feature、派生 Feature 和最终 Factor 的依赖 DAG。

### 3.3 Registry、Serving 与在线/离线存储

#### 资料 F：Feast

- [Feast Architecture](https://docs.feast.dev/v0.58-branch/getting-started/architecture/overview)
- [Feast Quickstart](https://docs.feast.dev/getting-started/quickstart)
- [Feast Online Store](https://docs.feast.dev/v0.58-branch/getting-started/components/online-store)

重点学习以下边界：

```text
Registry          保存定义、版本和元数据
Offline Store     保存历史并支持历史计算
Online Store      保存最新低延迟值
Materialization   将结果提升到在线存储
Feature Server    提供统一读取接口
Feature Service   组织消费者所需 Feature 集合
```

需要特别注意：Feast 不包办所有批处理和流处理计算。低延迟 Serving 的理想形态是把复杂计算提前完成，使在线读取退化为轻量键值查询。

建议产物：实现一个最小 `BatchGetLatestBefore` 接口，同时从 History 和 Current Projection 读取相同 Feature。

#### 资料 G：AWS SageMaker Feature Store

- [Feature Store Concepts](https://docs.aws.amazon.com/sagemaker/latest/dg/feature-store-concepts.html)
- [Online 与 Offline Store](https://docs.aws.amazon.com/sagemaker/latest/dg/feature-store.html)
- [Point-in-time Query](https://aws.amazon.com/blogs/machine-learning/build-accurate-ml-training-datasets-using-point-in-time-queries-with-amazon-sagemaker-feature-store-and-apache-spark/)

重点区分：

```text
Event Time           业务事实发生时间
API Invocation Time  系统收到写入请求的时间
Write Time           数据真正落盘的时间
```

Online Store 通常保存主体最新值，Offline Store 保存追加式历史。历史查询只能读取业务时间点之前可见的数据，否则会发生未来数据泄漏。

建议产物：构造一个迟到事件，验证它可以补入 History，但不能错误覆盖语义上更新的 Current 值。

### 3.4 元数据、治理与生产工程

#### 资料 H：Uber Palette / Michelangelo

- [Palette Meta Store Journey](https://www.uber.com/us/en/blog/palette-meta-store-journey/)
- [Michelangelo Machine Learning Platform](https://www.uber.com/us/en/blog/michelangelo-machine-learning-platform/)

重点学习：

- Feature 的搜索和发现；
- Owner 与责任边界；
- 定义与计算管道关联；
- 跨团队复用；
- 元数据、血缘和监控；
- 在线与离线一致性。

建议产物：为 Feature Definition 增加 Owner、版本、数据分类、新鲜度、来源、消费者和废弃状态。

#### 资料 I：Google Rules of ML

- [Rules of Machine Learning](https://developers.google.com/machine-learning/guides/rules-of-ml)
- [何时执行数据转换](https://developers.google.com/machine-learning/crash-course/production-ml-systems/transforming-data)

重点学习：

- 先建立可观测、可复现的基线；
- 保持训练和服务转换一致；
- 评估 Feature 的覆盖率、稳定性和成本；
- 避免难以稳定获取的生产 Feature；
- 防止 Training-serving Skew。

建议产物：为每个生产 Feature 建立 Freshness、Coverage、Null Rate、Drift 和 Serving Latency 指标。

## 4. 推荐学习路径

### 阶段一：理解风险信号

推荐资料：Fraud Detection Handbook、Stripe Radar、Amazon Fraud Detector。

需要回答的问题：

1. 当前请求有哪些原始事实？
2. 哪些风险只能通过历史窗口观察？
3. 哪些风险需要跨主体关系？
4. 标签从哪里来，多久才能确认？
5. 什么情况下 Feature 缺失、未定义或过期？

实践任务：

- 选择“短期大量入金后迅速出金”场景；
- 定义事件、主体、窗口和风险假设；
- 设计 `inflow_minor_24h`、`outflow_minor_24h`、`outflow_ratio_24h`；
- 明确定义除零、无历史数据、迟到事件和币种隔离语义。

验收产物：Feature Catalog 初稿和特征业务说明。

### 阶段二：掌握时间窗口和点时正确

推荐资料：Chronon、Feathr、AWS Point-in-time Query。

需要回答的问题：

1. `event_time`、`as_of`、`computed_at` 有什么区别？
2. 24 小时滚动窗口的边界是 `(as_of-24h, as_of]` 还是其他口径？
3. 迟到事件怎样修正 History？
4. 为什么历史回算不能直接使用当前值？
5. 批计算和流计算如何证明结果一致？

实践任务：

- 使用固定 `as_of` 计算历史窗口；
- 构造准时、允许迟到和过晚三类事件；
- 比较批处理与流处理的主体聚合；
- 记录差异、阈值和处理结果。

验收产物：时间语义契约、批流对账报告和迟到事件处理说明。

### 阶段三：建设 Registry 与 Serving

推荐资料：Feast、AWS Feature Store、Uber Palette。

需要回答的问题：

1. Feature Definition 和 Feature Value 分别保存在哪里？
2. Feature Set Key、Feature Key 与完整 Feature Reference 有什么区别？
3. History 与 Current Projection 有什么区别？
4. 在线读取如何选择最新但不晚于 cutoff 的 Snapshot？
5. 发布后的 Feature Definition 为什么必须不可修改？

实践任务：

- 建立 Feature Registry；
- 发布一个版本化 Feature Set；
- 发布 Feature Set，并让版本化 Feature Factor 直接引用其中的具体 Feature；
- 实现 `BatchGetLatestBefore` 和 `BatchGetAt`；
- 返回 `as_of`、`source_run_id`、内容指纹和新鲜度状态。

验收产物：Registry ER 图、读取接口契约、版本发布与回滚演练。

### 阶段四：接入风控决策

需要回答的问题：

1. Feature 与 Factor 的责任边界是什么？
2. 一次 Evaluation 应读取几次 Feature Store？
3. 当前请求数据是否要先写入 Feature Store？
4. Feature 缺失、过期、错误时规则如何处理？
5. 如何保存参与决策的 Feature Evidence？

实践任务：

- 根据 Feature Factor Definitions 临时编译 Fetch Plan，并批量读取完整 Feature References；
- 把 Feature 转换为类型化 Factor Values；
- 使用 CEL Rule 求值；
- 持久化 Evaluation、Feature Snapshot Reference 和 Actions；
- 验证相同请求重试不会产生重复决策或副作用。

验收产物：端到端风险决策证据链。

### 阶段五：生产治理

需要回答的问题：

1. 谁拥有这个 Feature？
2. 谁正在消费它？
3. Feature 是否新鲜、完整、稳定？
4. 新版本怎样 Shadow、灰度、回滚？
5. 下线 Feature 前怎样确认没有活跃消费者？

实践任务：

- 建立 Freshness、Coverage、Drift、Latency 指标；
- 建立流批差异告警；
- 演练 Feature Version 发布、废弃和退休；
- 演练 Online Store 故障时的降级；
- 使用历史 Evidence 重放一条风险决策。

验收产物：监控面板、发布 Gate、故障恢复和下线手册。

## 5. 推荐的阅读顺序

```text
Fraud Detection Handbook
        ↓
Stripe Radar
        ↓
Amazon Fraud Detector
        ↓
Chronon
        ↓
Feathr
        ↓
Feast
        ↓
AWS Point-in-time
        ↓
Uber Feature Governance
        ↓
Google Rules of ML
```

如果时间有限，优先精读四份：

1. Fraud Detection Handbook：理解风险特征如何产生；
2. Stripe Radar：理解真实生产风控需要什么信号；
3. Chronon：理解窗口、回算和流批一致；
4. Feast：理解 Registry、Serving 和 Materialization 边界。

## 6. 每份资料的阅读笔记模板

```text
资料名称：

它解决什么问题：

核心领域对象：

Feature 如何定义：

Feature 如何计算：

Feature 如何存储：

Feature 如何读取：

时间语义：

在线与离线一致性方案：

质量和监控方案：

适合直接借鉴的部分：

不适合当前系统的部分：

可以设计的实践实验：
```

## 7. 常见误区

### 误区一：Feature 就是数据库字段

数据库字段只有技术类型；生产 Feature 还需要业务含义、主体、窗口、单位、缺失语义、新鲜度、版本和血缘。

### 误区二：在线与离线要定义两套 Feature

在线和离线通常是计算、存储和读取方式不同，不应因此拥有两套互相漂移的业务语义。

### 误区三：当前请求必须先进入 Feature Store

当前请求值可直接作为 Value Factor；Feature Store 通常提供请求发生前的历史画像。需要“历史 + 当前请求”时，可由 Expression Factor 组合，避免等待窗口更新或重复计数。

### 误区四：Current 表就是历史真相

Current Projection 是可重建的快速读取视图；不可变 History 才负责回放、审计和点时查询。

### 误区五：Feature 更新自动触发所有风控规则

普通实时、近线和离线风控仍应由业务请求、消息或调度任务触发。Feature 更新事件更适合物化通知、缓存失效和持续监控等专门场景。

### 误区六：低延迟等于现场计算

复杂历史聚合通常应提前计算并物化。在线调用的理想形态是批量读取少量已经准备好的 Feature，而不是扫描业务库或现场执行大窗口聚合。

## 8. 最终学习成果

完成本路径后，学习者应当能够：

- 从风险用例推导 Feature，而不是从已有字段反推规则；
- 区分 Fact、Feature、Factor、Rule、Policy、Decision 和 Action；
- 正确定义主体、窗口、Event Time、`as_of` 和缺失语义；
- 设计 Current/History、Online/Offline Store 和 Materialization；
- 设计可版本化、可发布、可回滚的 Feature Registry；
- 解释并验证 Point-in-time Correctness；
- 验证批流一致性和迟到事件处理；
- 将 Feature 安全接入实时、近线和离线风险决策；
- 保存完整的决策证据和数据血缘；
- 通过监控、发布 Gate 和消费者关系治理 Feature 生命周期。

不建议直接照搬某一个开源 Feature Store。更适合规则型风控平台的组合是：

```text
Stripe 的风险信号方法
+ Chronon 的计算与时间语义
+ Feathr 的特征转换抽象
+ Feast 的 Registry / Serving 边界
+ AWS 的 Event Time / History 模型
+ Uber 的元数据与治理经验
```

最终目标不是“安装一个 Feature Store”，而是建立一套业务语义统一、时间正确、可复用、可审计且能稳定服务风险决策的特征平台。
