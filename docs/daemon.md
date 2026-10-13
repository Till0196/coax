# coaxd

`coaxd`は、チューナーを選局してストリームを流すデーモンです。
USB、Linux DVB API、Windows BDAといったOSごとの違いを内側で吸収し、クライアントにはどれも同じHTTP APIとして見せます。
選局とストリームを要求するのは`coax`、`BonDriver_coax`、ZeroWaveといったクライアントで、ハードウェアを制御するのはこのデーモンだけです。

デーモンを置くのは、1つのデバイスを開けるのが1つのプロセスだけだからです。
1枚のボードに複数のチューナーとカードリーダーが載っているので、デバイスを開いて各クライアントに割り当てるプロセスが必要です。
プロトコルと動作の仕様は[tuner-host.md](reference/tuner-host.md)にあります。

coaxが個別に対応しているUSBチューナーは、Linux、Windows、macOSのどれでも、メーカーのドライバーを使わずに直接制御します。
使うのはLinuxのusbfs（`/dev/bus/usb`）のioctl、WindowsのSetupAPIとWinUSB、macOSのIOKit（実行時に読み込みます）だけで、どれもOSに元から入っています。
どの経路も`CGO_ENABLED=0`でビルドでき、`coaxd`は外部のライブラリに依存しません。

すでに別のソフトで受信できている環境なら、ドライバーを入れ替える必要はありません。
`coaxd`はすべてのバックエンドを持ち、起動時にすべてのバックエンドで探索を行って、見つかったチューナーを1つの一覧にまとめます。
既存のソフトで受信できているなら、`coaxd`が使うドライバーもインストール済みです。
recpt1ならpx4_drvやpt3ドライバー、`dvbv5-zap`ならLinux DVBドライバー、BonDriver_PT3やBonDriver_BDAならEarthsoftのドライバーやメーカー製BDAドライバーです。
`coaxd`はそのドライバーをそのまま使います（recpt1やBonDriverのバイナリ自体は不要です）。
対応表と例外は[backends.md](setup/backends.md#既存の受信環境のドライバー)にあります。

## まず読むもの

| 知りたいこと | 文書 |
| --- | --- |
| 初めてのセットアップを通しで（フォルダの用意→ファームウェア→OSの準備→受信→いつものソフトから使う）| [docs/setup/getting-started.md](setup/getting-started.md) |
| 使うデバイスのバックエンドと検証状況 | [docs/setup/backends.md](setup/backends.md) |
| すでにrecpt1やdvbv5-zap、BonDriverで受信できている環境から移りたい | [docs/setup/backends.md](setup/backends.md#既存の受信環境のドライバー) |
| ファームウェアの入手と配置 | [docs/setup/firmware.md](setup/firmware.md) |
| HTTP API、デーモンの動作仕様、コマンドラインフラグ | [docs/reference/tuner-host.md](reference/tuner-host.md) |
| その他の文書一覧 | [docs/README.md](README.md) |

## 使い方

```sh
go build -o coaxd ./cmd/coaxd
./coaxd
# serving /run/user/1000/coaxd/http.sock
# for PC/SC, run coax pcsc; it makes the socket to point PCSCLITE_CSOCK_NAME at
```

通常は手動で起動する必要はありません。
[`coax`](../README.md)などのクライアントは、接続できなければ`coaxd`を起動します。
接続が無い状態が続くと、`coaxd`は終了します（既定15秒）。
常駐させたいときは`--idle 0`を付けます。
コマンドラインの全フラグは[docs/reference/tuner-host.md](reference/tuner-host.md#コマンドラインオプション)にあります。

デーモン側のコマンドは2つです（もう1つのコマンド`cmd/coax`は[本体README](../README.md)にあります）。

| コマンド | 内容 |
| --- | --- |
| `cmd/coaxd` | デーモン本体。HTTP APIとPC/SCのソケットを公開する |
| `cmd/fwextract` | メーカー配布のドライバーパッケージからファームウェアを取り出す。ハードウェアにはアクセスしない |

ファームウェアは同梱していません。
必要なファイルの入手と`fwextract`の使い方は[docs/setup/firmware.md](setup/firmware.md)にあります。

```sh
go build -o fwextract ./cmd/fwextract
./fwextract 202104_PX-W3U4_Driver.zip   # it930x-firmware.bin
```

## 対応チューナー

バックエンドは次のとおりです。
バックエンドごとの対応製品、対応チップ、ファームウェアの要否、実機での検証状況は[docs/setup/backends.md](setup/backends.md)に、デバイス固有の詳細は[ボード別文書](README.md#ボード別)にあります。

| 種別 | バックエンド |
| --- | --- |
| USBチューナー（直接USB）| `t230`、`px3`、`px4`、`pxmlt`、`isdb2056`、`s1ur`、`us3pout`、`fsusb2`、`fsusb2i`、`s270`、`cech-zd1j`、`tbs5520`、`tbs5520se`、`tbs5530` |
| OSのドライバー経由 | `dvb`（Linux DVB API）、`bda`（Windows BDA）、`chardev`（px4_drv、pt3ドライバー、pt1_drv）|
| Windowsでボードを直接制御する | `pt3`、`pt1`、`dd`、`tbs`（PCIe）|

C/Nや信号強度の取得元と、取得できるかどうかはバックエンドごとに異なります。
各ボード文書の「信号測定」の節を見てください。

## APIの記述

デーモンのHTTP APIはOpenAPI 3.1で記述してあり、`GET /openapi.json`で取得できます。
記述は`internal/coaxd/host/hostapi/swagger.json`にあり、デーモンに埋め込まれています。
APIのバージョン（`info.version`）はデーモンのバージョンとは別です。
既存のクライアントが無視できる追加なら末尾の数字だけを上げ、互換性が壊れる変更のときだけ先頭の数字を上げる予定です。
生成の仕組みは[docs/internals/architecture.md](internals/architecture.md#openapi記述の生成)にあります。

## 対応プラットフォーム

次のすべてで、`CGO_ENABLED=0`のままビルドとvetが通ります。

| OS | アーキテクチャ |
| --- | --- |
| Linux | amd64、386、arm、arm64（ほか、Goが対応するもの）|
| Windows | amd64、386、arm64 |
| macOS | amd64、arm64 |

どのバックエンドも、すべてのアーキテクチャ向けにビルドできます。

ただし、PT1/2/3などのPCI/PCIe接続のデバイスはメーカーのWindowsドライバーを使うので、そのアーキテクチャ向けのドライバーが無ければ使えません。
USB接続のデバイスはWindows標準のWinUSBを使うので、上の表のどのアーキテクチャでも使えます。

## 実装の構成

パッケージ構成、チューナー／受信ICの層の分け方、USBドライバーの再利用構造は[docs/internals/architecture.md](internals/architecture.md)にあります。
デーモンが動いているときの内部の動き（アクター構造、lease、ホットプラグ）は[docs/reference/tuner-host.md](reference/tuner-host.md)にあります。

## 制限事項

- Linux DVB経由で使える伝送方式は、カーネルのドライバーが`DTV_ENUM_DELSYS`で公開するものに限られます。
- Windows BDA経由で動くかどうかは、メーカーのBDAドライバーが標準のNetwork Providerに繋がるフィルター構成になっているかで決まります。デバイス固有の制御DLLが必須のデバイスは対象外です。
- 直接USBのバックエンドはLinux、Windows、macOSで動きます。どれも追加のライブラリは不要ですが、Windowsでは対象デバイスにWinUSBを割り当てる必要があります（[docs/setup/os.md](setup/os.md)）。
- 衛星の`FREQUENCY`は、既定では第1中間周波数として扱います。ダウンリンク周波数で書いたファイルも、libdvbv5と同じ`LNB`キーでLNBfを指定すれば使えます。バンドスタッキング方式のLNBfには対応していません。
- 日本の124／128度CSのLNB給電は11V／15Vです。Linux DVB APIには13V／18V／OFFしか無いので`dvb`バックエンドからは指定できず、日本仕様のボードか変換器が要ります。
- デバイスごとの伝送方式、LNB給電、22kHzトーン、DiSEqCの対応は[docs/reference/channel-files.md](reference/channel-files.md)にあります。
