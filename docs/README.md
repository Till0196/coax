# ドキュメント索引

全体の概要は[本体README](../README.md)にあります。
`coaxd`の設計の考え方（状態を持たないデーモンを、Mirakurun、TVTest、ZeroWaveが共有する構成）は[concepts.md](concepts.md)にあります。
MirakurunやTVTestなど、上位のソフトウェアとの関係は[stack.md](stack.md)にあります。

| ディレクトリ | 層 | 中身 |
| --- | --- | --- |
| [setup/](setup/) | 導入ガイド | 初回のセットアップ |
| [scanner/](scanner/) | スキャナー | `coax`側（スキャン、エクスポート、DVB-S2）|
| [reference/](reference/) | 共通リファレンス | バックエンドに依らない仕様。プロトコル、定義ファイル、カードリーダー |
| [boards/](boards/) | ボード別 | バックエンドごとの固有情報 |
| [internals/](internals/) | 実装 | 全体アーキテクチャとカーネル向けのパッチ |

## 導入ガイド

初めてセットアップするときは、上から順に読んでください。

| ドキュメント | 内容 |
| --- | --- |
| [concepts.md](concepts.md) | coaxのコンセプト。状態を持たないデーモン`coaxd`と、それを使うクライアント（Mirakurun、BonDriver、ZeroWave）の関係 |
| [getting-started.md](setup/getting-started.md) | セットアップの手順。フォルダの用意から受信まで |
| [firmware.md](setup/firmware.md) | ファームウェアの用意。`coaxd`の隣の`firmware`フォルダ、デバイスごとに必要なファイル、入手先、`fwextract`の使い方 |
| [os.md](setup/os.md) | OSごとの準備。Linuxの権限とunbind、WindowsのWinUSB割り当て |
| [backends.md](setup/backends.md) | バックエンドの一覧と選び方 |
| [mirakurun.md](setup/mirakurun.md) | Mirakurunから使う。`tuners.yml`の`command:`、`--system`の決まり方、`decoder`とPC/SCソケット |

## 共通リファレンス

| ドキュメント | 内容 |
| --- | --- |
| [tuner-host.md](reference/tuner-host.md) | デーモンのプロトコルと動作の仕様。エンドポイント、クエリ文法、レスポンス、エラー、チューナーID、コマンドラインフラグ、内部構造 |
| [channel-files.md](reference/channel-files.md) | 定義ファイルの書式。受理されるプロパティ、LNB給電、LNBf、利得、DiSEqC |
| [tune.md](reference/tune.md) | チューナーの一覧と選局。`coax list`と`coax tune`の書き方、チューナー名、衛星チャンネルの指定 |
| [tune-options.md](reference/tune-options.md) | 選局オプション。LNB給電、LNBf、利得、優先度、同期検査 |
| [satellite-channels.md](reference/satellite-channels.md) | 衛星チャンネルの書き方。`BS15_1`とStream IDの違い、ドライバー別に書ける形式 |
| [card-reader.md](reference/card-reader.md) | 内蔵カードリーダーの使い方。搭載デバイス、`coax card`、PC/SCソケット |
| [winscard.md](reference/winscard.md) | カードリーダーの実装。WinSCard互換API層とpcsc-liteプロトコル |

## ボード別

バックエンドごとの固有情報（対応製品、対応チップ、ファームウェア、定義ファイルの必要プロパティ、信号測定、制限事項）をボード単位で分けています。

| ドキュメント | バックエンド | 主な対象製品 |
| --- | --- | --- |
| [dvb.md](boards/dvb.md) | `dvb` | Linux DVBドライバーが`/dev/dvb`に公開するデバイス全般（PT1/2/3、Digital Devices、TBS、Siano、MyGica T230など、カーネル組み込みドライバーが当たる場合はこちら）|
| [t230.md](boards/t230.md) | `t230` | MyGica T230 / T230C / T230C Lite / T230A / T230C2 / T230C2 Lite |
| [px3.md](boards/px3.md) | `px3` | PLEX PX-S3U / S3U2 / W3U2 / W3U3 / W3U3 V2 |
| [px4.md](boards/px4.md) | `px4`、`pxmlt`、`isdb2056`、`s1ur` | PLEX / DigiBest製 IT930x系チューナー |
| [us3pout.md](boards/us3pout.md) | `us3pout` | DigiBest US-3POUT |
| [fsusb2.md](boards/fsusb2.md) | `fsusb2` | KEIAN KTV-FSUSB2（EM2874版）|
| [fsusb2i.md](boards/fsusb2i.md) | `fsusb2i` | KEIAN KTV-FSMINI / KTV-FSUSB2/V3（IT9175/IT9179版、K1212〜）|
| [s270.md](boards/s270.md) | `s270` | MyGica S270 / PLEX PX-S1UD |
| [cech-zd1j.md](boards/cech-zd1j.md) | `cech-zd1j` | Sony CECH-ZD1 J |
| [tbs.md](boards/tbs.md) | `tbs5520`、`tbs5520se`、`tbs5530` | TurboSight TBS 5520 / 5520SE / 5530（USB）|
| [tbs-pcie.md](boards/tbs-pcie.md) | `tbs` | TurboSight製PCIeボード（Windows専用）|
| [dd.md](boards/dd.md) | `dd` | Digital Devices製PCIeボード（Windows専用）|
| [bda.md](boards/bda.md) | `bda` | Windows BDA経由の対応チューナー全般 |
| [chardev.md](boards/chardev.md) | `chardev` | ptxキャラクタデバイス経由（px4_drv / pt3ドライバー / pt1_drv。Linux専用）。PLEX / DigiBest製チューナー、Earthsoft PT1 / PT2 / PT3 |
| [earthsoft-windows.md](boards/earthsoft-windows.md) | `pt3`、`pt1` | Earthsoft PT1 / PT2 / PT3（Windows専用。`coaxd`がボードを直接制御する）|

## スキャナー（`coax`側）

`coax`そのものの使い方は[本体README](../README.md)に、個別の話題は次に分かれています。

| ドキュメント | 内容 |
| --- | --- |
| [scan.md](scanner/scan.md) | スキャン。`--discover`、`--route`、ISDB-S3の書き方、J.83のannex差、中断と再開、解析時間の根拠、オプション一覧 |
| [export.md](scanner/export.md) | エクスポート。Mirakurun `channels.yml`、DVBv5 conf、BonDriver ChSet、TVTest ch2、EDCB ChSet4/5 |
| [dvb-s2.md](scanner/dvb-s2.md) | DVB-S2の放送。szap-s2jからの移行、トランスポンダ名、22kHzトーン、DiSEqC |
| [architecture.md](scanner/architecture.md) | `coax`側の実装の構成 |

## 実装

| ドキュメント | 内容 |
| --- | --- |
| [architecture.md](internals/architecture.md) | パッケージ構成と層の分け方 |
| [tc90522-isdbt-band.patch](internals/tc90522-isdbt-band.patch) | カーネルの`tc90522`がISDB-Tで申告する周波数範囲を広げるパッチ。`earth-pt3`経由でVHF、CATVを受信するときに当てます（[dvb.md](boards/dvb.md#earth-pt3が受け付ける周波数の範囲)）|
