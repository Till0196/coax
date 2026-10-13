# TurboSight TBS 5520 / 5520SE / 5530（バックエンド`tbs5520` / `tbs5520se` / `tbs5530`）

| | |
| --- | --- |
| バックエンド | `tbs5520`、`tbs5520se`、`tbs5530` |
| 対応OS | Linux / Windows / macOS（直接USB）|
| 伝送方式 | 製品による（下表）|
| ファームウェア | 必要（製品ごと。[firmware.md](../setup/firmware.md)）|

TBS独自のUSBブリッジを載せた外付けチューナーで、Linux、Windows、macOSから直接制御します。
実装はTBSの`linux_media`（`tbs5520.c`、`tbs5520se.c`、`tbs5530.c`）をもとにしています。

どの製品もブリッジのマイコンに不揮発メモリが無いので、接続のたびにブリッジのファームウェアを転送します。
復調器のファームウェアはそれとは別のファイルで、復調器の初期化時に読み込みます。

TBSのPCIeボードをWindowsで直接制御する`tbs`バックエンドは別のものです（[tbs-pcie.md](tbs-pcie.md)）。

## 対応製品

| バックエンド | 製品 | USB ID | チューナー | 構成 | 伝送方式 |
| --- | --- | --- | --- | --- | --- |
| `tbs5520` | TBS 5520 | `734c:5520` | 1 | AVL6882 + R848 | DVB-T、DVB-T2、DVB-C（J.83 Annex A）、DVB-S、DVB-S2 |
| `tbs5520se` | TBS 5520SE | `734c:5521` | 1 | Si2183 + Si2157（地上／CATV）／AV201x（衛星）| DVB-T、DVB-T2、ISDB-T、DVB-C（J.83 Annex A）、DVB-C/ANNEX_B、DVB-S、DVB-S2 |
| `tbs5530` | TBS 5530 | `734c:5530` | 1 | CXD2878ER（地上／CATV）＋ M88RS6060（衛星）| DVB-T、DVB-T2、DVB-C（J.83 Annex A）、DVB-C/ANNEX_B、ISDB-T、ATSC、DVB-S、DVB-S2 |

`tbs5520`と`tbs5530`は`DVBS2X`のチャンネルも受け付けます。
ただし対応方式として挙げるのはDVB-SとDVB-S2までなので、`coax list`にDVB-S2Xは出ません。

### TBS 5520

40MHz〜2.4GHzに対応する広帯域のチューナー1本ですべての方式を受信するので、アンテナ入力は1つです。
使える方式を決めているのはRF側ではなく復調器で、AVL6882は一度に1つのファームウェアしか実行できません。
方式の系統（地上、ケーブル、衛星など）をまたいで選局するとファームウェアを読み込み直すので、同じ系統の中での選局より大幅に遅くなります。

### TBS 5520SE

復調器はSi2183の1つです。
方式によって、地上用のSi2157と衛星用のAV201xのどちらを使うか、どのRF入力を使うかが決まります。
チューナーとしては1本です。

### TBS 5530

復調器を2つ載せています。
地上とケーブル用のCXD2878ERと、衛星用のM88RS6060で、どちらもチューナーを内蔵しています。
2つあるのは衛星と地上・ケーブルの両方を受信できるチップが無いためで、2チャンネルを同時に受信することはできません。

2つの復調器のTS出力は、1本のバルクエンドポイントの手前にあるセレクターに入り、どちらを繋ぐかはブリッジのGPIOで決まります。
多重化ではなく切り替えなので、選ばれなかった側のTSはUSBにまったく流れません。
そのため、この製品はチューナー1本として扱います。
どちらの復調器を使うかはチャンネルの伝送方式で決まり、`--tuner`で選ぶものではありません。
US-3POUTが2つの復調コアを1本のチューナーとして見せているのと同じ理由です。

TBS 5530の地上側の仕様は「DVB-T/T2/C/C2, ISDB-T/C, ATSC, J83B」ですが、coaxのCXD2878ERの実装で受信できるのは上の表の方式までです。
DVB-C2とISDB-Cは、復調器をその方式で初期化する手順を実装していません。

## ファームウェア

| バックエンド | ブリッジ用（毎回送る）| 復調器用 |
| --- | --- | --- |
| `tbs5520` | `dvb-usb-tbs5520-01.fw` | `dvb-demod-avl6882.fw` |
| `tbs5520se` | `dvb-usb-id5520se.fw` | `dvb-demod-si2183-b60-01.fw` |
| `tbs5530` | `dvb-usb-id5530.fw` | `dvb-demod-m88rs6060.fw`（衛星側だけ）|

TBS 5520SEの地上側のチューナーはSi2157なので、`dvb_driver_si2157_rom50.fw`／`dvb-tuner-si2157-a30-01.fw`も探します。
TBS 5530の地上側のCXD2878ERはファームウェアを使いません。
入手先と置き場所は[firmware.md](../setup/firmware.md)にあります。

## 定義ファイル

共通の書式（`FREQUENCY`のHzとkHz、`BANDWIDTH_HZ`の求め方、`STREAM_ID`の意味）と、バックエンドごとに必要なプロパティは[channel-files.md](../reference/channel-files.md)にあります。
この3製品に関わる分は次のとおりです。

衛星（`DVBS`、`DVBS2`、`DVBS2X`）の`FREQUENCY`はkHz、それ以外はHzとして読みます。
衛星では`SYMBOL_RATE`が必要です。

`tbs5520se`の`DVB-C/ANNEX_B`は、`SYMBOL_RATE`があればその値で受信します。
日本の光配信（5.6Msym/s）は`SYMBOL_RATE = 5600000`と書きます。
`SYMBOL_RATE`が無いときは変調方式で決め、`QAM/64`なら5.057Msym/s、それ以外は5.361Msym/s（256QAM）です。
Si2183にはLG系の復調器のような変調方式の自動判別が無いので、`SYMBOL_RATE`を書かない64QAMのチャンネルには`MODULATION = QAM/64`と書いてください。

`tbs5530`の`DVB-C/ANNEX_B`は`SYMBOL_RATE`が必須です。
5600000以上なら日本の光配信の5.6Msym/sの設定、それ未満なら北米の5.057／5.361Msym/sを復調器が選ぶ設定になります。

### LNB給電

衛星では給電の電圧がそのまま偏波の選択になるので、`POLARIZATION`があればそれで電圧が決まります（`H`/`L`が18V、`V`/`R`が13V）。
`lnb_power`／`VOLTAGE`は`POLARIZATION`が無いときの指定で、`low`が13V、`high`が18Vです。
`tbs5520se`は給電を切れないので、`off`はエラーになります。
ブリッジは2つの電圧のどちらかを選ぶだけで、給電そのものは配線で常に接続されているためです。
`tbs5530`は`off`にできます。

## 22kHzトーン

3製品とも出せます。
`tbs5520`（AVL6882）と`tbs5530`（M88RS6060）は、復調ICにトーン専用のレジスタがあります。
`tbs5520se`（Si2183）は、トーンもDiSEqCのコマンドで送ります。
このチップにはトーン専用のコマンドが無いので、長さ0のDiSEqCメッセージにトーンのビットを立てて送ります（`internal/coaxd/demod/si2183/sec.go`）。

## 使用例

```sh
./coax scan \
  --output scan-tbs5520se.json \
  ./channels-isdbt.conf

# TBS 5530で衛星をスキャンする。チューナーは1本なので指定は不要で、
# 復調器は定義ファイルの DELIVERY_SYSTEM で決まります。
./coax scan \
  --output scan-tbs5530-sat.json \
  ./channels-dvbs.conf
```

## 制限事項

- TBS 5530は復調器を2つ載せていますが、受信できるのは一度に1チャンネルです（2つのTSはセレクターで切り替わります）。
- TBS 5520は、方式の系統をまたぐたびに復調器のファームウェアを読み込み直します。
- `tbs5520se`は給電を切れません。
- OSごとの準備（Linuxの権限とunbind、WindowsのWinUSBの割り当て、macOSのIOKit）は[os.md](../setup/os.md)にあります。カーネルのドライバー（TBSの`linux_media`など）がデバイスを使用しているときは、先にunbindするか、ドライバーを読み込まない構成にしてください。WindowsでWinUSBを割り当てる`.inf`は[`driver/windows/tbs-winusb.inf`](../../driver/windows/tbs-winusb.inf)にあります（3製品分）。
