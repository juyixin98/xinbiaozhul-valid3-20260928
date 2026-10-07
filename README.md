# minkowski-service

纯后端组件：凸多边形的 Minkowski 和（Minkowski sum）与支撑方向查询
（support-direction query），C++20 + CMake + Boost.Multiprecision，
全部算术为精确有理数（`cpp_rational`），无浮点误差。

## 模块结构

```
include/geom/point.hpp      几何谓词层：精确有理点、叉积/点积、解析与打印
include/geom/polygon.hpp    ConvexPolygon：绕向统一(CCW)、共线边合并、
                            凸性校验、点/线段退化归约
include/algo/minkowski.hpp  空间算法层：边序列按极角循环归并的 Minkowski 和
include/algo/support.hpp    支撑查询：多边形 O(log n) 二分，退化形直接扫描
include/version.hpp         版本与算法标识（日志用）
src/main.cpp                批处理入口 minkcli（任务文件 / stdin）
tests/reference.hpp         独立参考实现：顶点对枚举 + Andrew 单调链凸包、
                            暴力支撑扫描（仅供测试，核心代码不依赖它）
tests/test_main.cpp         测试驱动：失败分类、运行身份、输入指纹、版本日志
fixtures/sample_job.txt     本地合成夹具任务
scripts/                    build/verify 的 Shell 与 PowerShell 脚本
```

模块关系：`geom`（谓词与形状）← `algo`（Minkowski / 支撑）←
`minkcli` 与 `mink_tests`。测试的期望值只来自 `tests/reference.hpp`，
不由被测核心自身生成。

## 算法假设

- 输入顶点按边界顺序给出（顺时针或逆时针均可），库内统一为 CCW；
  非凸、尖刺（自反边）输入抛出 `NonConvexError`。
- 相邻共线边在归一化时合并；全共线输入归约为线段，单点归约为点。
- 退化形的 Minkowski 和走同一边归并框架：点=平移（空边环），
  线段=边环 `[d, -d]`；结果再经归一化完成共线合并与退化归约。
- 支撑函数满足可加性 `h_{A+B}(d) = h_A(d) + h_B(d)`，
  CLI 的 `additivity` 命令与测试均以此作为独立校验。

## 依赖与版本

- 编译器：C++20（本机验证 g++ 13.3）
- CMake >= 3.20（本机 3.28.3）
- Boost >= 1.74，仅头文件组件 Multiprecision（本机 1.83）
- 无容器、无额外语言运行时；脚本仅 Shell 与 PowerShell。

## 本地运行

```sh
./scripts/build.sh                 # 配置 + 编译（不跑测试）
./scripts/verify.sh                # 编译 + 跑测试 + 跑样例任务
./build/minkcli fixtures/sample_job.txt
./build/mink_tests --run-id my-run # 或 ctest --test-dir build
```

PowerShell：`scripts/Build.ps1`、`scripts/Verify.ps1`。

## 判定方式

- 测试：每个用例打印 `[TEST <id>] PASS/FAIL`，失败带
  `category=assertion|unexpected-exception|missing-exception`（隐含在
  断言日志中），结尾 `[SUMMARY] ... failed=0` 且退出码为 0 才算通过；
  日志含 run id、库版本、算法标识、Boost 版本与每个输入的指纹哈希。
- CLI：`ADDITIVITY` 行必须为 `status=OK`；非凸输入退出码 3，
  任务格式错误退出码 2，可加性不一致退出码 4。

## 已知限制

- 输入必须按边界顺序给出，不做任意点集重排（乱序点集请先求凸包）。
- 支撑点在一批方向并列最大时返回边转移处的顶点（值唯一，点不唯一）。
- 测试与样例数据均为本地合成夹具，未接入真实业务数据。
