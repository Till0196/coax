# Earthsoft PT1 / PT2 / PT3（Windows、バックエンド`pt3` / `pt1`）

| | |
| --- | --- |
| バックエンド | `pt3`（PT3）、`pt1`（PT1 / PT2）|
| 対応OS | Windows専用（Linuxは[chardev.md](chardev.md)）|
| 伝送方式 | ISDB-T、ISDB-S |
| ファームウェア | 不要。EarthsoftのドライバーとSDKが必要（下記）|

PT1、PT2、PT3にはBDAドライバーがありません。
Earthsoftが配布しているのは、汎用のPCIカーネルドライバー（Jungo WinDriver）とC++のSDKだけで、DirectShowのフィルターグラフには出てきません。
そのため`bda`バックエンドからはチューナーとして見えません。

そこでWindowsでは、`coaxd`がWinDriverを通してボードを直接制御します。

## 前提

| ボード | 必要なもの | 名前、ライセンスの読み出し元 |
| --- | --- | --- |
| PT3 | Earthsoftの[PT3-Driver](https://earthsoft.jp/PT3/download_SHA-2.html)とPT3-SDK | `SDK_EARTHSOFT_PT3.dll` |
| PT1 / PT2 | `PT1-Driver`または`PT2-Driver`と`PT1-PT2-SDK` | `SDK_EARTHSOFT_PT1_PT2.dll` |

ドライバー名とライセンス文字列はSDKのDLLから読むので、`coaxd`側の設定は不要です。
SDKが無い場合、ボードは一覧に表示されませんが、エラーにはなりません。

- PT3はFPGAのバージョンが0x04のものだけを使います。ほかのバージョンはレジスタの意味が異なる可能性があるため、開く時点でエラーにします。
- PT3とPT1/PT2、PT1とPT2を、1台のPCで同時に使えます。

## 一覧と名前

1枚が4つのチューナーとして並びます。
名前はPCI上の位置から作るので、スロットを動かさないかぎり変わりません。

```
pt3:6-17-0-0:0  ISDB-S  Earthsoft PT3 satellite tuner 1
pt3:6-17-0-1:0  ISDB-S  Earthsoft PT3 satellite tuner 2
pt3:6-17-0-2:0  ISDB-T  Earthsoft PT3 terrestrial tuner 1
pt3:6-17-0-3:0  ISDB-T  Earthsoft PT3 terrestrial tuner 2
```

WinDriverの制約で、ボードは1回しか開けません。
4つのチューナーは1つのハンドルを共有し、最後の1本が使い終わったときに閉じます。
一覧を作るときにはボードを開きません。

## Linux経路との違い（PT3）

| | Linux（pt3ドライバー、[chardev.md](chardev.md)）| Windows（このバックエンド）|
| --- | --- | --- |
| 周波数の指定 | ドライバーのチャンネル番号に直して渡す | 周波数をそのままチューナーへ渡す。地上波の1/7MHzのオフセットもテーブルではなく計算で求める |
| `STREAM_ID` | 0〜7の相対番号だけ | TSIDも、12未満なら相対スロットも受け付ける（復調器のTMCCで解決）。省略すると相対スロット0 |
| UHF 62ch | 受けられない | 受けられる（周波数で指定するので、上限は803MHz）|
| 信号強度 | 読めない | 地上波は読める（MxL301RFのRSSI、dBm）。衛星側はチップに測る手段が無い |
| C/N | 生カウンタをioctlで受け取って換算 | 復調器から直接読んで換算（同じ曲線）|
| 欠落の検知 | 知らされない | DMAのリングを1周追い越されたらオーバーランとして数える。ボード自身が数える不良パケット数も読める |

定義ファイルの共通の項目と、LNB給電の既定（`VOLTAGE`を省略すると給電しない、`low`は11V、`high`は15V）はLinuxと同じです（[channel-files.md](../reference/channel-files.md)）。

## PT1 / PT2固有の事情

同じEarthsoftの製品でも、PT1 / PT2とPT3では、復調器（TC90522系）以外の作りが違います。

| | PT3 | PT1 / PT2 |
| --- | --- | --- |
| FPGA | Altera | Xilinx Spartan-3 |
| PCI ID | `1172:4C15` | `10EE:211A`（PT1）／`10EE:222A`（PT2）|
| メモリ窓 | 2つ | 1つだけ |
| I2C | 4ビット命令を詰めた列をアップロードして開始アドレスで起動 | 1命令ずつ。命令が「格納先アドレス＋2本の線の状態＋次の命令アドレス」を丸ごと持つ連結リスト |
| ROM上のプログラム | あり（復調器と地上波チューナーの初期化）| なし。すべて`coaxd`の側で行う |
| DMA | チャンネルごとに独立したディスクリプタリング | ボードに1つ。511ページのページテーブルを環状に繋ぎ、クレジット方式で流量制御 |
| 出てくるもの | そのままTS | 4本を混ぜた32ビットのマイクロパケット列。63個で188バイト1個 |
| チューナー | QM1D1C0042 / MxL301RF | QM1D1B0004 / TDA6651 |
| 信号強度 | ISDB-Tだけ読める | どちらも読めない（チップに測る手段が無い）|
