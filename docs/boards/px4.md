# PLEX(px4以降) / DigiBest製チューナー（バックエンド`px4` / `pxmlt` / `isdb2056` / `s1ur`）

| | |
| --- | --- |
| バックエンド | `px4`、`pxmlt`、`isdb2056`、`s1ur` |
| 対応OS | Linux / Windows / macOS（直接USB）|
| 伝送方式 | ISDB-T、ISDB-S（`pxmlt`はISDB-C、DVB-T/T2、DVB-S/S2、DVB-C/ANNEX_A、DVB-C/ANNEX_Bも）|
| ファームウェア | 要（`it930x-firmware.bin`。[firmware.md](../setup/firmware.md)）|

[px4_drv](https://github.com/nns779/px4_drv)が対応しているデバイスを、Linux、Windows、macOSから直接制御します。
どれもIT930xのUSBブリッジを載せていて、その先の復調器とチューナーをI2Cで操作します。
全チューナーのTSは1本のバルクエンドポイントにまとめて転送されるので、`tsdemux`がチューナーごとに分けます。

OSごとの準備は[os.md](../setup/os.md)にあります。
WindowsでWinUSBを割り当てる`.inf`は、バックエンドごとに`px4-winusb.inf`、`pxmlt-winusb.inf`、`isdb2056-winusb.inf`、`s1ur-winusb.inf`として[`driver/windows/`](../../driver/windows)にあります。
px4_drvなどのカーネルドライバーがデバイスにバインドされているときは、先にunbindするか、ドライバーを読み込まない構成にしてください。

## 対応製品

| バックエンド | 製品 | VID:PID | チューナー本数 | 伝送方式 |
| --- | --- | --- | --- | --- |
| `px4` | PX-W3U4 | `0511:083f` | 4（ISDB-S×2、ISDB-T×2）| ISDB-T、ISDB-S |
| `px4` | PX-Q3U4 | `0511:084a` | 4×2デバイス | ISDB-T、ISDB-S |
| `px4` | PX-W3PE4 | `0511:023f` | 4（ISDB-S×2、ISDB-T×2）| ISDB-T、ISDB-S |
| `px4` | PX-Q3PE4 | `0511:024a` | 4×2デバイス | ISDB-T、ISDB-S |
| `px4` | PX-W3PE5 | `0511:073f` | 4（ISDB-S×2、ISDB-T×2）| ISDB-T、ISDB-S |
| `px4` | PX-Q3PE5 | `0511:074a` | 4×2デバイス | ISDB-T、ISDB-S |
| `pxmlt` | PX-MLT5U | `0511:084e` | 5 | ISDB-T、ISDB-S、ISDB-C、DVB-T、DVB-T2、DVB-S、DVB-S2、DVB-C/ANNEX_A、DVB-C/ANNEX_B |
| `pxmlt` | PX-MLT5PE | `0511:024e` | 5 | 同上 |
| `pxmlt` | PX-MLT8PE | `0511:0252`＋`0511:0253` | 3＋5の2デバイス | 同上 |
| `pxmlt` | DigiBest ISDB6014（4TS）| `0511:0254` | 4 | 同上 |
| `pxmlt` | e-Better DTV02A-5TS-P（PX-MLT5PEと同じ作り）| `0511:924e` | 5 | 同上 |
| `isdb2056` | DigiBest ISDB2056 | `0511:004b` | 1（ISDB-T/S切替）| ISDB-T、ISDB-S |
| `isdb2056` | DigiBest ISDB2056N | `0511:084b` | 1（ISDB-T/S切替）| ISDB-T、ISDB-S |
| `isdb2056` | PLEX PX-M1UR | `0511:0854` | 1（ISDB-T/S切替）| ISDB-T、ISDB-S |
| `s1ur` | PLEX PX-S1UR | `0511:0855` | 1 | ISDB-T |
| `s1ur` | DigiBest ISDBT2071（DTV03A-1TU）| `0511:0052` | 1 | ISDB-T |

| バックエンド | 復調器 | チューナー |
| --- | --- | --- |
| `px4` | TC90522 | RT710（衛星）／R850（地上）|
| `pxmlt` | CXD2856ER | CXD2858ER |
| `isdb2056` | TC90522 | RT710（衛星）／R850（地上）|
| `s1ur` | TC90522 | R850 |

`px4`の4本のチューナーは、0と1がISDB-S、2と3がISDB-Tです。
`--tuner N`で選びます。

PX-Q3U4、PX-Q3PE4、PX-Q3PE5は中で2つのUSBデバイスに分かれていて、それぞれを4チューナーの別のデバイスとして扱います。
PX-MLT8PEも2つのデバイスに分かれています。
Q系の2つのデバイスは同じシリアル番号なので、チューナー名は必ず`シリアル@ポート経路`の形になります（[tuner-host.md](../reference/tuner-host.md)）。

### px4_drv経由で使う場合

ここに挙げたデバイスは、px4_drvを入れて、そのドライバー経由で使うこともできます（バックエンド`chardev`、[chardev.md](chardev.md)）。
ただし、同じデバイスを両方から同時には使えません。
px4_drvが読み込まれていると、px4_drvがUSBインタフェースを使用するので、このバックエンドは`EBUSY`で開けません。
どちらを使うかの決め方は[chardev.md](chardev.md#px4_drvと直接usbのどちらを使うか)にあります。

## ファームウェア

IT930xの`it930x-firmware.bin`が必要です。
入手先と置き場所は[firmware.md](../setup/firmware.md)にあります。
PLEXが配布しているWindowsドライバーのパッケージを`fwextract`に渡すと取り出せます。
復調器とチューナーのIC（TC90522、CXD2856ER、CXD2858ER、R850、RT710）にはファームウェアは要りません。

`fwextract`は、ドライバーの中にあるセグメントのテーブルからイメージの終わりを求めます。
そのため、px4_drvの`fwtool`が持つオフセットのテーブルに載っていない版のドライバーからも取り出せます。
イメージの中身（USBスキャッター書き込みのパケットの並び）は、[fsusb2i.md#ファームウェア](fsusb2i.md#ファームウェア)に同じ形式の説明があります。

## 定義ファイル

共通の書式は[channel-files.md](../reference/channel-files.md)にあります。
ここでは、これらのバックエンドだけの分を書きます。

### `STREAM_ID`

ISDB-Sの`STREAM_ID`は、小さい値をTMCCの相対スロット番号、それ以上をTSIDとして扱います。
境目は復調器で違い、`px4`と`isdb2056`（TC90522）は12、`pxmlt`（CXD2856ER）は8です。
省略すると相対スロット0を選びます。
DVB-T2では、`STREAM_ID`をPLP ID（0〜255）として読みます。

### `pxmlt`のケーブル受信（ISDB-C / DVB-C / J.83 Annex B）

`pxmlt`は、ISDB-CとDVB-C/ANNEX_Aを同じ復調コア（モードバイト`0x04`）で受信します。
Annex AとAnnex Cの違いはロールオフ（15%と13%）だけで、モードの選択から見れば同じものです。
シンボルレートは書かなくて構いません。
復調器には帯域から決まる探索範囲だけを渡すので、64QAMと256QAMのチャンネルが混ざった定義ファイルもそのまま使えます。
帯域は6MHzだけで、8MHzを書くとエラーになります（チップのJ.83のフィルターが6MHz用の1組しか無いためです）。

```ini
[CABLE-EXAMPLE]
	DELIVERY_SYSTEM = ISDB-C
	FREQUENCY = 111142857
```

J.83 Annex B（`DVB-C/ANNEX_B`）は、トレリス符号化を使う別の復調コア（モードバイト`0x08`）で受信します。
こちらは`SYMBOL_RATE`が必要です。
5600000以上なら日本の光配信の5.6Msym/sの設定、それより小さければ北米の5.057／5.361Msym/sを復調器が選ぶ設定になります。
帯域はこちらも6MHzだけです。

```ini
[CATV-J83B-EXAMPLE]
	DELIVERY_SYSTEM = DVBC/ANNEX_B
	FREQUENCY = 112750000
	SYMBOL_RATE = 5600000
```

### `GAIN`

`GAIN`が効くのは、`pxmlt`と、`px4`／`isdb2056`の衛星側です。

`pxmlt`のCXD2858ERは、地上と衛星それぞれの入力に外付けの増幅器を持っています。
増幅器は入れるか切るかだけで、チップが利得の値を公表していないので、`min`／`max`／`auto`だけを受け付けます（`dB`での指定は受け付けません）。
起動時は両方とも入っています。

`px4`／`isdb2056`の衛星側（RT710／RT720）は、IF段のFineGainで0／1／2／3dBです。
RT720は3dBと2dB、1dBと0dBがそれぞれ同じ設定になるので、0／3dBの2段として出します。
どのボードも3dB（最大）で起動します。

地上側のR850には利得の設定がありません。
LNA、RF、ミキサーの利得はどれも周波数帯ごとの決まった値から選ばれ、`GAIN`を指定するとエラーになります。
`s1ur`も同じです。
R850にはRF利得のモードを切り替える機能がありますが、これは値を決めるものではなく、AGCを止めて今の値に固定するものなので、`min`／`max`／`dB`のどれにも当てはまりません。

### LNB給電

LNB給電は、ボードで共有のGPIOで入れたり切ったりするだけで、電圧は選べません。
`low`と`high`はどちらも「給電する」、`off`は「給電しない」で、指定しなければ給電しません（[tuner-host.md](../reference/tuner-host.md)）。
`off`は「このチューナーは給電を要求しない」という意味で、ほかの衛星チューナーが給電を使っている間はそれを切りません。

## 信号測定

| バックエンド | C/N | 信号強度 |
| --- | --- | --- |
| `px4`、`isdb2056` | TC90522の生のカウンターを変換式に通した値 | 衛星側だけ。RT710が返す入力レベル（dBm）。地上側は出ない |
| `pxmlt` | CXD2856ERから、方式ごとの変換式に通した値 | CXD2856ERのAGCから |
| `s1ur` | TC90522から | 出ない |

## 制限事項

- `pxmlt`のDVB-T/T2は、復調器のバンドの定数が20.5MHzの水晶用の値のままです。この部品の水晶は24MHzなので正しくありませんが、24MHzで正しく動かすには、テーブルを差し替えるだけでは足りません。詳しくは`internal/coaxd/demod/cxd2856er/modes.go`のコメントにあります。
- `pxmlt`のISDB-Tのタイミングの公称レートには、`8 × 水晶周波数 ÷ シンボルレート`から計算した値を書いています。詳しくは`internal/coaxd/demod/cxd2856er/modes.go`のコメントと`modes_test.go`にあります。
- CXD2858ERのJ.83で使うPLLの基準分周比は16MHzの水晶用の値です。4種類の水晶の値は`internal/coaxd/tuner/cxd2858er/dvb.go`のコメントと`dvb_test.go`にあります。
- `px4`／`isdb2056`／`s1ur`の地上側に`GAIN`はありません。
