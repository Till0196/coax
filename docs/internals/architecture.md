# アーキテクチャ

`coaxd`の内部の作りを説明します。
使うだけなら読む必要はありません。
`coaxd`の実行時の動作（アクター構造、ホットプラグ、lease）は、[tuner-host.md](../reference/tuner-host.md)の「内部構造」以降にあります。

## 公開面

外部から使えるのは`cmd/`のコマンドだけで、`coaxd`の実装はすべて`internal/coaxd/`にあります。
クライアントとのやり取りをHTTPだけにするために、意図的にこの構成にしています。
Goのパッケージを共有しないので、デーモンとクライアントのどちらか一方だけを入れ替えられ、Go以外で書かれたクライアントもGoのものと同じ扱いになります。

| コマンド | 内容 |
| --- | --- |
| `cmd/coaxd` | チューナーホスト本体。HTTPとPC/SCのインタフェースを提供する |
| `cmd/coax` | クライアントCLI。`coaxd`とはHTTPだけで通信する（実装は`pkg/`）|
| `cmd/fwextract` | メーカー配布のドライバーパッケージからファームウェアを取り出す。ハードウェアにはアクセスしない（[firmware.md](../setup/firmware.md#fwextract)）|

## パッケージ構成

| パッケージ | 内容 |
| --- | --- |
| `host/*` | デーモンそのもの。所有権のプール（`pool`）、HTTPハンドラー（`server`）、API型とOpenAPI（`hostapi`）、ソケットとアイドル終了（`daemon`）、安定ID（`tunerid`）、Linux DVBとWindows BDAの取り込み（`native`）、Windowsの通知領域（`tray`）|
| `device` | チューナーのドライバーが実装するBoard、Tuner、Streamのインタフェースと、デバイスや電源の参照カウント。ファームウェアの探索もここ |
| `receiver` | OSやデバイスのAPIに依存しない、共通の受信インタフェース |
| `i2c` | バスとその先のICをつなぐインタフェース。復調器のゲート越しのアクセスと、マスク付き書き込みの共通処理 |
| `tuning` | 伝送方式名の正規化とチャンネル定義プロパティの解釈。LNB給電、LNBf、GAIN、DiSEqCもここ |
| `demod/*` | 復調IC。`avl6882`、`cxd2856er`、`cxd2857er`、`cxd2878`、`m88rs6060`、`mb86a20s`、`mxl58x`、`si2168`、`si2183`、`sony`（Sony系で共通のコア）、`stid135`、`stv091x`、`tas2101`、`tc90522` |
| `tuner/*` | チューナーIC。`av201x`、`cxd2858er`、`fc0012`、`mxl136`、`mxl301rf`、`nm120`、`qm1d1b0004`、`qm1d1c0042`、`r848`、`r850`、`rda5816`、`rt710`、`si2157`（Si2141とSi2157を1つのドライバーで扱う）、`stv6120`、`tda18211`、`tda20142`、`tda6651` |
| `smartcard/*` | 内蔵カードリーダーのIC（`asie5606`）と、ISO/IEC 7816-3のT=1（`iso7816`）。ICはバイト列をそのまま通すだけなので、ブロックの組み立て、LRC、分割された応答の組み立てもここで行う |
| `usb/*` | 対応USBチューナーのドライバー一式（[下記](#usbチューナーの層)）|
| `dvb` | Linux DVB APIでの列挙、選局、フロントエンド状態取得（[dvb.md](../boards/dvb.md)）|
| `chardev` | px4_drv、pt3ドライバー、pt1_drvのキャラクタデバイス（ptx ioctl）での列挙、選局、TSの読み出し |
| `pcie/*` | Windowsでボードを直接制御する。`pt3`、`pt1`は`windriver`経由、`dd`、`tbs`は`bda`経由 |
| `pcie/windriver` | Jungo WinDriverのカーネルサービスをIOCTLで直接呼ぶ層。ユーザーモードのDLL（`wdapi1200.dll`）は読み込みません（[理由](../boards/earthsoft-windows.md#pt1--pt2固有の事情)）|
| `bda/*` | Windows BDAの受信一式。`filters`が列挙、`graph`がフィルターグラフとTune Request、`sink`がTSを受け取る自前のレンダラー、`mode`が復調モードの切り替え、`tuning`がチャンネル記述、`cards`がボード単位の占有管理、`vendors/*`がベンダー固有の制御、`comutil`がCOMの補助 |
| `win32` | DirectShow／COMバインディング（自前）|
| `winscard`、`pcscd` | WinSCard/PC-SC互換API層と、pcsc-liteのクライアントプロトコル実装（[winscard.md](../reference/winscard.md)）|
| `mpegts`、`tlv` | MPEG-2 TSとMMT/TLVの同期検査 |
| `fwextract` | ドライバーパッケージからのファームウェア抽出 |
| `build` | バージョンとコミットの埋め込み |

## チューナーのインタフェースと受信IC

`device`、`i2c`、`tuning`、`demod/*`、`tuner/*`、`smartcard/*`は、デバイスの接続方法に依存しない層です。
`usb`の外に置いているのは、PCIeボードを直接制御するときにも同じものを使うためです。
Boardが1つのハードウェア、Tunerがその上の1本のチューナーで、開く前に分かる情報は`TunerInfo`が持ちます。

ICのドライバーは`i2c.Bus`だけに依存し、そのICの接続先には依存しません。
USBブリッジの先にあるのか、PCIeボードのFPGAのI2Cブロックの先にあるのかの違いは、バスの実装が吸収します。

## USBチューナーの層

`usb`配下は、同じICを載せた製品どうしでドライバーを再利用できるよう層に分けています。

- `usb/usbio`：control、bulk、isochronousの転送
- `usb/usbdev`：USB固有の登録情報だけを持つ`Backend`。どのベンダーID・プロダクトIDで見えるかと、開かずに分かるチューナー構成を宣言します
- `usb/boardkit`：`device`のインタフェースのうち、製品によらない部分（チューナーの排他的な確保、参照カウント、閉じるときのストリーム停止、読み取りタイムアウトの変換）を担当する層。製品側は選局、状態の取得、ストリームの開始と停止だけを実装します
- `usb/tsdemux`：1本のエンドポイントに多重化された複数チューナーのTSを分ける
- `usb/bridge/*`：USBブリッジIC（`asv5211`、`dvbsky`、`em287x`、`it930x`、`it9175`、`tbs`、`tbs5520`、`tbs5520se`、`tbs5530`）。`siano`だけはI2Cバスを持たず、チップ上のファームウェアとやり取りするメッセージプロトコルを実装しています
- `usb/board/*`：製品ごとにブリッジ、復調器、チューナーを組み合わせる層。`plex`はIT930x系製品で共通の土台（ファームウェア読み込み、電源とLNBの参照カウント、ストリーム分離）です
- `usb`：製品の一覧と、バックエンド名の解決

新しいチューナーは、多くの場合`usb/board/`にパッケージを1つ足し、`usb/usb.go`の一覧に1行加えるだけで対応できます。
未対応のICを載せている場合だけ、`usb/bridge/`、`demod/`、`tuner/`にドライバーを足します。

1台の製品に複数のチューナーが載ることがあるので、Board（USBデバイス）とDevice（チューナー1本）の2段に分けています。
USBデバイスは参照カウントで共有されるので、あるチューナーを閉じても、受信中の別のチューナーには影響しません。
IT930xのように、全チューナーのTSを同期バイト付きで1本のエンドポイントに多重化するブリッジでは、`tsdemux`が1つの読み取りループからチューナーごとのバッファへ振り分けます。

ボードを開いたり閉じたりするのは`host/pool`だけです。
`host/pool`が1つのボードにつき1つのハンドルを持ち、接続ごとにチューナーを貸し出します。
ボードはチューナーより長く開いておき、最後の利用者が解放してからもしばらく（既定10秒）は閉じません。
内蔵カードリーダーもチューナーと同じプールから取得するので、そのボードのチューナーが使用中でもカードだけを使えます。

## OSのドライバー経由のバックエンド

Linux DVBとWindows BDAのチューナーは、`host/native`がチューナー1本だけのボードとして扱い、USBチューナーと同じ一覧、同じ貸し出しの仕組みに載せます。
Linuxでは、PCIeのチューナーだけでなく、Linux DVBのドライバーが`/dev/dvb`に公開するUSBドングルも同じ`dvb`バックエンドで使えます。

`chardev`は、Linux DVB APIではなく、recpt1が使う「ptx」のioctlを呼びます。
px4_drv、pt3ドライバー、pt1_drvの3つがこの経路です。
ioctlの番号もチャンネル番号の体系も3つで同じなので、1つのバックエンドとして扱い、受信できる範囲と選局の細かい違いだけをドライバーごとに分けています。
どのドライバーが作ったノードかは、ノードの名前で見分けます。
クライアントに関係する違い（`STREAM_ID`が相対番号か識別子か）は`GET /tune`の`relative_streams`が数値で返すので、クライアントがバックエンド名で分岐する必要はありません。
DVBのフロントエンドと同じく1つのノードは1つのプロセスしか開けないので、同じプールに載せます。
px4_drvが使用中のデバイスはUSBバックエンドからは開けないので、USB側の一覧からは除外し、1台が二重に並ばないようにしています。

PT1 / PT2 / PT3には、カーネル組み込みのDVBドライバー（`earth-pt1`、`earth-pt3`）を使う経路もあり、その場合は`dvb`バックエンドでそのまま使えます。
どちらを使うかは[chardev.md](../boards/chardev.md#カーネル組み込みdvbドライバーという選択肢pt1--pt2--pt3)にまとめてあります。

## OpenAPI記述の生成

デーモンのHTTPインタフェースはOpenAPI 3.1で記述していて、`GET /openapi.json`で読めます。
記述は`internal/coaxd/host/hostapi/swagger.json`としてコミットしてあり、デーモンに埋め込まれています。
APIのバージョン（`info.version`）はデーモンのバージョンとは別です。
既存のクライアントが無視できる追加なら末尾の数字だけを、互換性のない変更なら先頭の数字を上げる予定です。

記述は、ハンドラーと`hostapi`の型から生成します（`go generate ./internal/coaxd/host/server/`）。
生成器は実行時には使わないので、`coaxd`のバイナリに入る外部の依存はありません。
生成し忘れると、`internal/coaxd/host/server/openapi_test.go`がルートのテーブル、受け付けるパラメーター、JSONに出る型の全フィールドと突き合わせて、`go test ./internal/coaxd/host/server/`を失敗させます。
