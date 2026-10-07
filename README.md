# 凸包最小面积外接矩形

本项目读取二维整数点，求凸包的最小面积外接矩形，并返回精确角点、面积、接触点证据以及可重放的逐方向判断日志。

## 依赖

- C++20 编译器（GCC 11+ 或 Clang 14+）
- CMake 3.20+
- Boost 1.74+，仅使用 header-only 的 `Boost.Multiprecision`

构建不需要容器、账号或网络数据。建议通过系统包管理器固定 Boost，例如 Ubuntu 上使用 `libboost-dev`；CMake 通过 `find_package(Boost 1.74 REQUIRED)` 锁定最低版本。

## 构建

```sh
cmake -S . -B build -DCMAKE_BUILD_TYPE=Release
cmake --build build --parallel
```

运行示例：

```sh
./build/mer_batch examples/fixtures.txt
./build/mer_generate_fixture diamond | ./build/mer_batch
```

PowerShell：

```powershell
.\scripts\run_examples.ps1
```

Shell：

```sh
./scripts/run_examples.sh
```

## 输入协议

批处理使用面向行的文本协议：

```text
BEGIN diamond
POINT 0 -1
POINT 1 0
POINT 0 1
POINT -1 0
END
```

- `BEGIN id`：开始一个案例，`id` 是字符串。
- `LIMIT n`：可选，声明本案例允许的最大点数，必须出现在任何 `POINT` 前。
- `POINT x y`：声明一个 `int64` 坐标点。ID 由输入顺序自动分配，从 0 开始。
- `END`：结束并计算当前案例。
- 空行和以 `#` 开头的行会被忽略。

输出是 JSON Lines，每行一个独立案例。失败案例也产生一条 JSON 记录，不会自动终止后续案例。

## 精确性和误差

几何判断分层处理：

1. 凸包、投影极值、面积比较、接触点归属使用 `boost::multiprecision::cpp_int`，没有浮点近似。
2. 矩形角点和面积以精确分数返回，分母是边长平方，永不使用零长度边作为分母。
3. `cpp_dec_float_100` 只用于最终十进制输出和最终误差界。
4. 近共线谓词是诊断接口，按 `|cross| / edge_length <= tolerance` 判断；凸包仍使用精确 orientation，点不会因容差被错误丢弃。

最终十进制误差界保守使用 `10^-90 * (1 + |value|)`。它表示内部高精度十进制转换和打印的舍入区间，不是精确整数谓词的容差。

## 面积并列规则

所有候选面积都以精确分数比较：

```text
area_num / edge_length_squared
```

面积较小时直接选择；面积相等时保留凸包边索引更小的候选。凸包按确定的字典序和逆时针规则生成，因此相同输入下结果稳定。

## 接触证据

成功记录包含：

- `corners`：四个逆时针矩形角点，含分数和十进制值。
- `corner_contact_ids`：每个角点接触的原始点 ID。
- `edge_log`：对每条凸包边方向记录方向向量、切向/法向投影极值、面积分数、四个接触边集合、是否选中和判断理由。
- `run_number`、`case_id`、`begin_line`：用于重放具体案例。

## 错误类别

输出使用可区分的 `error_category` 和具体 `error_code`：

- `INPUT_ERROR`：非数字、坐标越界、空案例、未知命令等。
- `STATE_CONFLICT`：嵌套 `BEGIN`、`END` 无 `BEGIN`、先有点后声明 `LIMIT`、零长度谓词参考边等。
- `RESOURCE_EXHAUSTED`：案例数或点数上限、内存分配失败。
- `COMPUTATION_FAILED`：内部零长度边、没有候选等不应由正常输入触发的计算问题。

## 退化案例

- 空案例在批处理协议中是输入错误；库 API 对空向量返回 `empty`。
- 单点或全部重复点：四个角点重合，面积 `0/1`，不执行除法。
- 共线点：退化为线段矩形；方向分母使用非零线段长度平方，宽为零，面积 `0/1`。
- 近共线点：精确凸包仍保留有效顶点；只有显式调用近共线诊断时才应用容差。

## 项目结构

```text
include/mer/        公共数据类型、几何算法和批处理契约
src/                精确谓词、凸包、旋转卡壳、JSONL 批处理
apps/               mer_batch 命令行入口
tools/              本地合成夹具生成器
tests/              独立参考枚举测试和错误契约测试
examples/           可直接批处理的示例
docs/               设计说明
```

## 测试

`tests/test_reference.cpp` 不使用被测核心生成参考答案：它独立枚举所有点对方向，重新计算投影极值、面积，并对每个候选检查所有点包含。测试还覆盖单点、重复、共线、近共线、面积并列、批处理输入/状态错误和 JSON 错误类别。

构建后可执行：

```sh
ctest --test-dir build --output-on-failure
```

批处理交付环境本次按要求未执行测试套件；测试源码和目标已随工程生成。
