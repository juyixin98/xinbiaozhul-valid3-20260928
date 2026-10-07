# 大规模折线段最近投影与里程定位服务

C++20 / CMake / Boost.Multiprecision 实现的本地后端工程：在整数格点折线上做
精确最近投影与里程（chainage）定位，支持批处理入口与退化案例验证。
所有数据均为本地合成夹具，无生产账号与真实业务数据。

## 模块关系

```
src/geometry/    几何层（不依赖空间索引）
  types          cpp_int 格点向量、精确有理数 Rat、标签脱敏（FNV-1a 指纹）
  predicates     精确方向谓词、线段关系分类、点-段精确平方距离、AABB 下界
  polyline       折线模型与前缀里程表（零长段保留里程语义）
  projection     候选与查询结果类型（Accepted / Rejected / Indeterminate）
src/spatial/     空间算法层
  brute_force    全段扫描参考引擎（独立的地面真值代码路径）
  bvh_index      线段 AABB 静态 BVH，best-first 精确下界剪枝
  candidate_set  两个引擎共用的候选集收尾（稳定排序、里程填充）
src/validate/    退化案例验证（零长段、重复顶点、自交、巨大坐标、空折线）
src/batch/       批处理入口：请求校验、引擎分派、带请求标识的脱敏诊断
src/io/          本地夹具文件格式解析与结果写出
apps/            mileage_query / mileage_batch / mileage_validate / gen_fixtures
tests/           独立测试（自带微型断言框架，无额外测试依赖）
scripts/verify.sh  本地验证入口
data/            生成的合成夹具（gen_fixtures 输出，不入库）
```

## 算法假设

- 坐标为十进制整数（任意大，`boost::multiprecision::cpp_int`）；所有距离
  比较在精确有理数上进行，浮点仅用于里程输出（`long double`）。
- 零长段是合法段：投影参数 `t=0`，里程等于该顶点处的累计链age，可为
  最终候选。
- 并列（精确等距）时返回全部并列候选，按（段号, t）稳定排序。
- BVH 剪枝只丢弃下界严格大于当前最优的节点，且下界本身用整数精确
  计算，因此不会丢失精确最优或并列候选；结果与全段扫描逐位一致。
- 查询坐标绝对值超过 `2^126` 时返回 `INDETERMINATE`（无法给出可信浮点
  里程），空折线返回 `REJECTED`。
- 自交检测用索引做 AABB 候选对生成、精确谓词确认；相邻段共享端点视为
  正常连接，不上报。

## 诊断与脱敏

每个请求输出一行诊断到 stderr：`[req=<id>] label=fnv8:<指纹>
decision=<ACCEPTED|REJECTED|INDETERMINATE> reason=<...> candidates=<k>
engine=<index|scan> nodes_visited=... nodes_pruned=...`。
自由文本标签视为敏感数据，只打印 FNV-1a 指纹，绝不打印原文；验证报告
中的坐标只打印数量级。

## 依赖版本

- GCC 13.3（或任何支持 C++20 的编译器）
- CMake >= 3.20
- Boost >= 1.74（仅头文件，本机验证为 1.83）

## 本地验证

```sh
./scripts/verify.sh
```

预期判断方式：

1. `ctest` 全部 5 个测试通过（谓词 / 投影手算值 / 索引对照扫描 / 退化
   案例 / 批处理与脱敏）。
2. `results_index.txt` 与 `results_scan.txt` `diff` 为空——索引引擎与
   全段扫描在 5 万段折线、202 个查询上结果完全一致。
3. `mileage_validate` 对 `self_intersect.poly` 报告 1 处自交（段 0 与
   段 3），对 `zero_length.poly` 报告 2 个零长段与 1 个重复顶点游程。
4. 批处理 stderr 中每个请求都有 `req=` 标识与 `decision=` 及原因；
   超限坐标查询为 `INDETERMINATE`。

也可以单条查询：

```sh
build/mileage_query data/simple.poly 12 4
# R cli ACCEPTED reason=ok candidates=1 | seg=1 t=2/5 dist2=4/1 mileage=14.000000
```
