# バックエンド一覧と選び方

`coaxd`は、チューナーまでの経路を「バックエンド」という単位で扱います。
バックエンドは、経路（直接USB、OSのドライバー経由、Windowsでボードを直接制御する）と対象製品の組み合わせで、全部で21種あります。

使うデバイスがどのバックエンドになるかは、この文書の表で確認してください。
バックエンドごとの詳細（対応チップ、定義ファイルの固有プロパティ、信号測定、制限事項）は各ボード文書にあります。

## 3つの経路

| 経路 | 必要なもの | 対応OS |
| --- | --- | --- |
| 直接USB | メーカーのドライバーは不要です。デバイスによってはファームウェアファイルが必要です（[firmware.md](firmware.md)）| Linux / Windows / macOS |
| OSのドライバー経由 | そのドライバーがインストールされていること（Linux DVB、px4_drv、pt3/pt1ドライバー、Windows BDA）| ドライバーによる |
| Windowsでボードを直接制御する | メーカーのWindowsドライバー（ボードのレジスタへのアクセスにだけ使います）| Windowsのみ |

直接USBの経路で使うのは、Linuxではusbfs（`/dev/bus/usb`）のioctl、WindowsではSetupAPIとWinUSB、macOSではIOKitで、どれもOS標準の機能です。
OSごとの設定は[os.md](os.md)にあります。

## 直接USBバックエンド

| バックエンド | 主な対象製品 | ファームウェア | 文書 |
| --- | --- | --- | --- |
| `t230` | MyGica T230 / T230C / T230C Lite / T230A / T230C2 / T230C2 Lite | 要 | [t230.md](../boards/t230.md) |
| `px3` | PLEX PX-S3U / S3U2 / W3U2 / W3U3 / W3U3 V2 | 要 | [px3.md](../boards/px3.md) |
| `px4`、`pxmlt`、`isdb2056`、`s1ur` | PLEX / DigiBest製 IT930x系チューナー（PX-W3U4、PX-MLT5PE、PX-M1UR、PX-S1URなど）| 要 | [px4.md](../boards/px4.md) |
| `us3pout` | DigiBest US-3POUT | 不要 | [us3pout.md](../boards/us3pout.md) |
| `fsusb2` | KEIAN KTV-FSUSB2（EM2874版）| 不要 | [fsusb2.md](../boards/fsusb2.md) |
| `fsusb2i` | KEIAN KTV-FSMINI / KTV-FSUSB2/V3（IT9175/IT9179版、K1212〜）| 要 | [fsusb2i.md](../boards/fsusb2i.md) |
| `s270` | MyGica S270 / PLEX PX-S1UD | 要 | [s270.md](../boards/s270.md) |
| `cech-zd1j` | Sony CECH-ZD1 J | 不要 | [cech-zd1j.md](../boards/cech-zd1j.md) |
| `tbs5520`、`tbs5520se`、`tbs5530` | TurboSight TBS 5520 / 5520SE / 5530（USB）| 要 | [tbs.md](../boards/tbs.md) |

## OSのドライバー経由のバックエンド

| バックエンド | 経由するもの | 主な対象 | 文書 |
| --- | --- | --- | --- |
| `dvb` | Linux DVB API（`/dev/dvb`）| Linux DVBドライバーが対応するデバイス全般（PCIeボードもUSBドングルも）| [dvb.md](../boards/dvb.md) |
| `chardev` | px4_drv（キャラクタデバイス）| PLEX / DigiBest製チューナー（Linux専用）| [chardev.md](../boards/chardev.md) |
| `chardev` | pt3ドライバー（m-tsudo/pt3）| Earthsoft PT3（Linux）| [chardev.md](../boards/chardev.md) |
| `chardev` | pt1_drv | Earthsoft PT1 / PT2（Linux）| [chardev.md](../boards/chardev.md) |
| `bda` | Windows BDA | メーカー製BDAドライバーを持つチューナー全般 | [bda.md](../boards/bda.md) |

Linuxでは、他の表に載っているボードの多くが、実際にはこの`dvb`バックエンドになります。
Digital DevicesやTBSのPCIeボード、PT1/PT2/PT3、Siano系のスティックにはどれもLinuxのDVBドライバーがあり、そのドライバーが読み込まれている間は、ドライバーがデバイスを使用するためです。
どのドライバーがどのデバイスを扱うかの対応表は[dvb.md](../boards/dvb.md#このバックエンドで扱うデバイス)にあります。

## Windowsでボードを直接制御するバックエンド

BDAドライバーが無いか、BDAでは足りない制御が必要なPCI/PCIeボードのための経路です。
どれもWindows専用です。

| バックエンド | 主な対象製品 | 文書 |
| --- | --- | --- |
| `pt3` | Earthsoft PT3（WinDriverを通して`coaxd`が直接制御する）| [earthsoft-windows.md](../boards/earthsoft-windows.md) |
| `pt1` | Earthsoft PT1 / PT2（同上）| [earthsoft-windows.md](../boards/earthsoft-windows.md) |
| `dd` | Digital Devices製PCIeボード | [dd.md](../boards/dd.md) |
| `tbs` | TurboSight製PCIeボード | [tbs-pcie.md](../boards/tbs-pcie.md) |

## 既存の受信環境のドライバー

`coaxd`はすべてのバックエンドを持ちます。
バックエンドを1つ選んで起動するのではなく、起動時にすべてのバックエンドで探索を行うので、`chardev`と`dvb`と直接USBのチューナーが1台のマシンに混在していても、1つの一覧に表示されます。

そのため、基本的には既存の受信環境のドライバー構成を変える必要はありません。
既存のソフトで受信できているなら、`coaxd`が使うドライバーもインストール済みです。

| 既存のソフト | ドライバー | `coaxd`でのバックエンド |
| --- | --- | --- |
| recpt1（PLEX / DigiBest製）| px4_drv | `chardev` |
| recpt1（PT3）| pt3ドライバー | `chardev` |
| recpt1（PT1 / PT2）| pt1_drv | `chardev` |
| dvbv5-zap、w_scan、tvheadendなど | Linux DVB（`/dev/dvb`）| `dvb` |
| recisdb-rs | px4_drvまたはLinux DVB（環境による）| `chardev`または`dvb` |
| BonDriver_PT3-ST、BonDriver_PT3（Windows）| Earthsoft PT3-Driver＋PT3-SDK | `pt3`（Windows専用。WinDriver経由）|
| BonDriver_BDA（Windows）| メーカー製BDAドライバー | `bda` |

recpt1やdvbv5-zapそのものは不要です。
`coaxd`が使うのはその下のドライバーで、ドライバーとのやり取り（px4_drvのキャラクタデバイス、ptxのioctl、DVB API、BDAのフィルターグラフ）は`coaxd`が行います。
BonDriverも同様で、`coaxd`はBonDriverのDLLを読み込みません。
BonDriver_PT3が動く環境にはEarthsoftのドライバーとSDKがインストールされているので、`coaxd`はそれらを直接使います。

チャンネル定義ファイルも作り直す必要はありません。
`coaxd`が受け取るのはDVBv5の定義（`dvbv5-zap`が読むもの）で、recpt1式のチャンネル名（`BS15_1`、`27`）も`coax tune --ch`で使えます。

### そのまま使えない場合

- **同時には使えません**：ドライバーが同じでも、1つのデバイスを開けるプロセスは1つだけです。recpt1で録画している間は、`coaxd`から同じチューナーを開けません（逆も同様です）。
- **px4_drvがバインドしているIT930x系のデバイス**は、直接USBのバックエンドからは開けません。`coaxd`はこれを検出して、そのデバイスを直接USBの一覧から除き、`chardev`としてだけ表示します。対応範囲の広い直接USB（[下記](#同じデバイスに複数の経路があるとき)）に移行する場合にだけ、unbindが必要です。
- **Windows BDAには条件が2つあります**：64ビットのBDAドライバーであることと、標準のNetwork Providerに繋がるフィルター構成であることです。BonDriver_BDAは32ビットのことが多く、デバイス固有の制御DLLが前提の構成も扱えますが、そうした構成は`coaxd`の`bda`バックエンドの対象外です（[bda.md](../boards/bda.md#前提条件)）。

## 同じデバイスに複数の経路があるとき

一部のデバイスは、直接USB（または直接制御する経路）とOSのドライバー経由の両方で使えます。
同じデバイスを2つの経路から同時に使うことはできません。

- **PLEX / DigiBest製（IT930x系）**：px4_drvを入れて`chardev`で使うか、unbindして直接USB（`px4`ほか）で使うか。比較表は[chardev.md#px4_drvと直接usbのどちらを使うか](../boards/chardev.md#px4_drvと直接usbのどちらを使うか)にあります。対応する伝送方式、信号強度の取得、内蔵カードリーダーの対応は直接USBのほうが広く、`chardev`には、ドライバーがインストール済みの環境ならファームウェアを用意しなくてよい利点があります。
- **PT1 / PT2 / PT3（Linux）**：専用ドライバー経由（pt3ドライバー／pt1_drvで、どちらも`chardev`バックエンド）と、カーネル組み込みのDVBドライバー（`earth-pt3`／`earth-pt1`）経由の`dvb`バックエンドがあります。経路の選び方は[chardev.md](../boards/chardev.md#カーネル組み込みdvbドライバーという選択肢pt1--pt2--pt3)、カーネルの`earth-pt3`がVHF/CATVを受け付けない制約は[dvb.md](../boards/dvb.md#earth-pt3が受け付ける周波数の範囲)にあります。
- **Digital Devices / TBSのPCIeボード、Siano系スティック、MyGica T230系**：Linuxではカーネル（またはベンダー配布）のDVBドライバー経由の`dvb`バックエンドになります。`dd`と`tbs`（PCIe）はWindows専用なので、Linuxでの選択肢は`dvb`だけです。詳しくは[dvb.md](../boards/dvb.md#このバックエンドで扱うデバイス)にあります。
- **カーネルのドライバーがバインドしているUSBデバイス**は、直接USBのバックエンドからは`EBUSY`で開けません。unbindの手順は[os.md](os.md#カーネルドライバーからのunbind)にあります。

## 信号測定値の取得可否

C/Nや信号強度の取得元と、取得できるかどうかはバックエンドごとに異なります。
詳しくは各ボード文書の「信号測定」の節にあります。
C/Nはほとんどのバックエンドで取得できますが、信号強度を取得できるのは一部（`t230`、`fsusb2i`、`px4`系の衛星側、`pxmlt`、`dd`、`tbs`の多く、Windowsの`pt3`の地上側など）だけです。
