# 凸多边形 Minkowski 和与支撑方向查询服务

这是一个纯后端、行协议批处理服务。坐标和查询方向均为任意精度整数
`boost::multiprecision::cpp_int`，叉积、点积、Minkowski 顶点加法全部为精确运算，
因此输入被限制为整数。

## 构建与运行

```bash
./scripts/build.sh
./scripts/generate-fixtures.sh
./scripts/run-example.sh
./scripts/verify.sh
```

也可直接执行：

```bash
cmake -S . -B build/native -DCMAKE_BUILD_TYPE=Debug -DBUILD_TESTING=ON
cmake --build build/native --parallel
./build/native/minkowski_service --config config/geometry-service.conf < examples/batch.txt
ctest --test-dir build/native --output-on-failure
```

预期判断：

- 正常批处理输出 `VERIFY PASS`、`OK SUPPORT`，最后进程退出码为 0。
- `examples/expected-failures.txt` 中的凹多边形应输出
  `ERROR ... reason=NON_CONVEX`，进程退出码为 1；后续命令在默认配置下继续执行。
- CTest 每个测试输出 `[summary] passed=N failed=0`，总体验证返回 0。

## 行协议

- `RUN <id>`：设置日志中的运行身份。
- `POLY <name> x y [x y ...]`：输入点或线段；三个及以上点按多边形边界绕向解释。
- `SUM <out> <a> <b> [VERIFY]`：计算 Minkowski 和；`VERIFY` 使用独立的
  “枚举两两顶点和 + Andrew 单调链凸包”参考答案核验。
- `TRANSLATE <out> <input> dx dy [VERIFY]`：平移并核验。
- `SUPPORT <name> dx dy`：返回精确支撑值以及全部最大化顶点。
- `VERIFY_SUPPORT <a> <b> dx dy`：验证 `h(a)+h(b)=h(a+b)`。
- `SHOW`、`LIST`、`CONFIG`、`HELP`、`QUIT`：查看与控制命令。

## 算法假设

1. 多边形由不同顶点构成，接受顺时针或逆时针输入，输出统一为逆时针、
   字典序最小顶点开头。
2. 输入中连续共线点在规范化时合并；这不改变几何集合。
3. 点、线段和二维多边形是不同 `ShapeKind`。所有点共线时规范化为线段。
4. 空集单独表示；对空集求支撑方向会报错，因为支撑函数未定义。
5. 线段 Minkowski 和使用前向、后向两条有向边；同向边合并，反向边保留。
6. 方向 `(0,0)` 返回值 0，并列出当前表示中的全部极值顶点，即所有规范化顶点。
7. 协议不提供删除或覆盖确认，当前会话中重复定义同名集合会覆盖旧值。

## 模块关系

- `include/minkowski/predicates.h`、`src/predicates.cpp`：精确点积、叉积、朝向、
  边极角比较。
- `convex_set`：绕向统一、凸性校验、共线点合并、退化形状分类。
- `minkowski`：边向量合并算法和点平移。
- `support`：线性点积扫描及全部最大化顶点返回。
- `convex_hull` 与 `verification`：独立单调链参考答案、Minkowski 核验、
  平移核验和支撑函数相加核验。
- `protocol` 与 `main`：本地标准输入/输出批处理入口。
- `config` 与 `logger`：独立配置和带运行 ID、步骤号、时间戳、判定依据的日志层。
- `tools/generate_fixtures.cpp`：本地合成夹具生成器，不依赖外部业务数据。
- `tests/`：七个独立 C++20 测试程序。

## 依赖版本

- C++20 编译器；已按 GCC/Clang 常用选项配置。
- CMake 3.20 或更高版本；仓库提供 CMake 3.28 可使用的 presets。
- Boost.Multiprecision 头文件，最低声明为 Boost 1.74；本机检测到 Boost 1.83。
- 无网络服务、数据库、容器、消息队列或额外语言运行时。

## 已生成但未运行的测试

`test_predicates`、`test_polygon_validation`、`test_minkowski`、
`test_support`、`test_degeneracy`、`test_reference`、`test_protocol`
均已写入 CTest。根据本批运行约束，本次交付没有执行测试或声称测试已通过。
