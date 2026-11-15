# NekoBox++ PC

NekoBox++ 的桌面（Windows）版，与安卓版同一套规矩、同一个 sing-box 内核 fork。

绿色免安装：解压后双击 `NekoBoxPlusPlus.exe` 即用，数据保存在同目录 `data.json`。

## 功能

- 订阅导入/自动更新，新节点只测新来的（地区＋延迟，分批 10 个）
- 节点按地区分区；分区可开「自动模式」：备选轮换只刷记录，在连节点真实流量三次拨号失败才自动切换
- 规则分流：规则可绑（分组，国家）分流池；内置商店优先＋中国域名/IP 直连，默认开启
- 网络变化（换网/换地址）自动全量重扫地区，只扫地区不测速
- 连接方式：TUN 模式（需管理员）/ 系统代理模式
- 内核：onlyno999/sing-box fork（流量归属统计＋拨号健康补丁），以库形式内嵌

## 构建

GitHub Actions（windows-latest）打包：Actions → Build NekoBoxPlusPlus PC → Run workflow。
本地开发需要同级目录有 `../sing-box-fork`（go.mod replace 指向它）。
