# WinSCard API層（`winscard`）

内蔵カードリーダーは、OSからPC/SCのリーダーとして認識されません。
この層は、内蔵カードリーダーをアプリケーションが期待するAPIで提供します。WindowsではWinSCard、それ以外ではpcsc-liteのAPIです。
この層はAPIの提供だけを担当します。
使い方は[card-reader.md](card-reader.md)にあります。

## 構成

| 場所 | 役割 | 状態 |
| --- | --- | --- |
| `pkg/winscard` | WinSCard APIそのもの。ハンドル、列挙順、振り分け、キャンセル、ステータスコード | 実装済み |
| `internal/winscard/usbcard` | 内蔵リーダーのプロバイダ。リーダー名、デバイスを開いておく期間、共有とトランザクション、T=1のセッション | 実装済み |
| `pkg/winscard/native` | Windowsのリソースマネージャーへ転送するプロバイダ（System32の`WinSCard.dll`を絶対パスで読み込む）| 実装済み、実機では未確認 |
| `internal/winscard/daemoncard` | 別のプロセスや別の機械の`coaxd`へHTTPで転送するプロバイダ | 実装済み |
| `pkg/pcscd` | pcsc-liteのクライアントプロトコルを実装したサーバー | 実装済み、libpcsclite 2.3.3で確認 |
| `cmd/coax`の`pcsc` | `pkg/pcscd`を待ち受けさせるコマンド。ソケットはクライアント側が作る | 実装済み |
| `WinSCard.dll`互換のDLL | Windowsのアプリケーションに読み込ませるDLL | 実装済み。`coaxd`のHTTPのカードAPIに直接接続する |

`pkg/winscard`は、プロバイダによらない共通の処理を持ちます。
ハンドルの管理（`ContextHandle`と`CardHandle`）、列挙順、リーダー名の重複の除去、リーダー名によるプロバイダへの振り分け、キャンセル、`SCardGetStatusChange`の分割と合流、`SCARD_S_SUCCESS`から`SCARD_W_*`までのステータスコードです。
呼び出し規約とバッファの受け渡しは持ちません。
それらはバインディングが担当します。

## プロキシとしての構成

`Service`はプロバイダを順序付きのリストで持ちます。
この層はドライバーではなく、プロキシとして動作します。

```text
アプリケーション
    |
    | winscard.Service
    v
Provider（並びが列挙順）
    ├ 内蔵リーダー   → device.CardReader → card        （usbcard）
    ├ 外付けリーダー → OSのリソースマネージャー        （native）
    └ coaxd越し      → coaxdのHTTP                      （daemoncard）
```

`daemoncard`は、`coaxd`の`/cards/{id}/connect`、`/transmit`、`/transaction`、`/events`へ転送します。
カードはそれを開いたHTTP接続に属するので、開いたカード1枚につき接続を1本持ちます。
`Service`を使う側からは、3つのプロバイダを区別できません。
`coax pcsc`のソケットは、カードがローカルのマシンにあってもリモートのマシンにあっても同じように応答します。

OSの実装の代わりにこの層を読み込ませても、外付けリーダーを引き続き使えるようにする必要があります。
そのために`native`のプロバイダがあります。

内蔵リーダーを先に並べる順序は、設定では変更できません。
この層を使うこと自体が、内蔵リーダーを使う指定になっているためです。
また、「カードが入っている最初のリーダー」を選ぶ実装は、並び順が固定されていることを前提にしています。

## 動作の規則

| 事項 | 動作 | 理由 |
| --- | --- | --- |
| プロバイダの確立 | 最初に必要になったときに行う | デバイスが接続されていない、pcscdが動いていない、といったプロバイダの問題で、他のプロバイダだけを使う呼び出しが失敗しないようにするため |
| 列挙での失敗 | 他のプロバイダが応答した場合、失敗したものは無視する | 同上。すべて失敗したときだけ最初のエラーを返す |
| ハンドル | 値からプロバイダを推測できないようにする | ハンドルのテーブルだけで対応を管理するため |
| `SCardDisconnect`の失敗 | それでもハンドルは無効にする | 接続はすでに失われている。再試行を許すと、破棄したセッションへAPDUを送れてしまう |
| `SCardCancel` | 進行中の待機だけを解除する。後の呼び出しには持ち越さない | 待機していないコンテキストへのキャンセルを、次の呼び出しが引き継いでしまう実装があるため |
| 未知のリーダーを待つ | `SCARD_STATE_UNKNOWN`と`STATE_CHANGED`を立ててすぐ返す | 取り外されたリーダーを問い合わせたアプリケーションが、この応答を期待しているため |
| `\\?PnP?\Notification` | 全プロバイダへ渡す | リーダーの増減がどのプロバイダで起きるかは事前に分からないため |

## Windowsとpcsc-liteで値が違う定数

定数はwinscard.hに合わせています。
pcsc-liteの`pcsclite.h`／`reader.h`と違うのは次の3つだけです。

| 定数 | Windows | pcsc-lite |
| --- | --- | --- |
| `SCARD_PROTOCOL_RAW` | `0x00010000` | `0x0004` |
| `SCardStatus`の状態 | `SCARD_ABSENT`〜`SCARD_SPECIFIC`の通し番号（1〜6）| ビットフラグ（`0x0002`〜`0x0040`）。`SCARD_UNKNOWN`もWindowsでは0、pcsc-liteでは`0x0001` |
| `SCARD_SCOPE_GLOBAL` | 無い | `0x0003` |

`ProtocolRaw`はどのプロバイダも返しません。
対象のリーダーはT=1にしか対応していないためです。
`ScopeGlobal`は受け付けます。
pcsc-liteはスコープを参照しないので、拒否する理由がありません。

変換が必要なのは、`SCARD_*`の値を外部に返す箇所だけです。
Windowsの`WinSCard.dll`として提供するときは、この層の値をそのまま使えるので変換は不要です。
pcsc-liteのクライアントと通信するときは変換が必要です。
値を返しているのは`pkg/pcscd`で、変換もここで行います（状態をビットフラグにする、`SCARD_PROTOCOL_T1`を`0x0002`として書く、など）。

## 内蔵リーダーのプロバイダの動作

| 呼び出し | 内蔵リーダーの応答 |
| --- | --- |
| `Connect` | `ProtocolT1`を含まない要求は`SCARD_E_PROTO_MISMATCH`。共有モードが`ShareShared`と`ShareExclusive`以外（`ShareDirect`を含む）なら`SCARD_E_INVALID_VALUE`。カード無しで接続する手段が無いためで、`ShareDirect`を扱うのは転送するプロバイダだけ |
| `Status` | `CardSpecific`（T=1を確立済み）、`CardPresent`（挿さっているが初期化前）、`CardAbsent`の3つだけ。プロトコルは常に`ProtocolT1` |
| `Transmit` | `sendProtocol`が`ProtocolT1`でなければ`SCARD_E_INVALID_PARAMETER`。長さが0か`AttrMaxInput`を超える場合も同じ |
| `GetAttrib` | `ATR_STRING`、`CURRENT_PROTOCOL_TYPE`、`ASYNC_PROTOCOL_TYPES`、`MAXINPUT`、`ICC_PRESENCE`、`VENDOR_NAME`、`VENDOR_IFD_TYPE`、デバイス名の4つ（`DEVICE_FRIENDLY_NAME`と`DEVICE_SYSTEM_NAME`のA/W）。それ以外は`SCARD_E_UNSUPPORTED_FEATURE` |
| `SetAttrib` | 常に`SCARD_E_UNSUPPORTED_FEATURE`。属性の値はどれもハードウェアかカードが決めていて、変更できるものが無い |
| `Control` | `SCARD_E_UNSUPPORTED_FEATURE` |

`Disconnect`、`EndTransaction`、`Reconnect`に渡すDispositionは、`LeaveCard`ならセッションをそのまま残します。
`ResetCard`と`UnpowerCard`はどちらもセッションを破棄します。
カードの電源だけを切る手段が無く、どちらの場合も次はリセットから始まるので、結果は同じです。
`EjectCard`は`SCARD_E_UNSUPPORTED_FEATURE`です。
カードの排出機構を持つデバイスはありません。

未対応の属性へのエラーは、この層では`ErrUnsupportedFeature`にそろえ、Windowsのエラー値への置き換えはバインディングの境目で行います。
Goの層にプラットフォーム固有のコードを持ち込まないためです。

`SCARD_ATTR_MAXINPUT`（`0x0007A007`）は、仕様が定めたクラスではなくベンダー定義のクラスの属性ですが、応答しています。
T=1のブロックをホスト側で組むカードインタフェースは任意の長さのAPDUを受け付けられず、クライアントがそれを知る手段はこの属性だけだからです。
値は250で、フレームからプロローグと誤り検出を引いた長さのうち、最も小さい組み合わせです。
一部のカードでしか通らない値を返さないよう、最小値にしています。
1つのAPDUを複数のIブロックに分けて送ることには対応していません。

## T=1のブロックを組む場所

どの経路でも、T=1のブロックは`pkg/iso7816`が組みます。
NAD、PCB（送信の通し番号）、長さ、APDU、LRCはすべてホスト側で作ります。
「カードインタフェース」を持つデバイスでも、CCIDリーダーのようにAPDUを渡すだけで済むものはありません。

違いはその下の層にあります。
IT930xのUARTはブリッジが生のバイト列しか転送しないので、ブロックの切り出し、再送、ATRの終端の判定までホスト側で行います。
残りの4つ（ASIE5606、EM2874、IT9175、EM287x）はブロック単位で受け渡しし、受信した長さも専用のレジスタから読めます。

ATRは、長さでは終端を判定できません。
受信長のレジスタが受信中も増え続けるためで、受信したバイトを毎回解析し、ATRとして構造が完結したかで判定します。
ATRのTA1から通信速度を、TA3／TB3／TC3からブロックサイズ、待ち時間、誤り検出の種類（LRCかCRC）を読みます。
ブロックサイズの交渉と再同期の順序はTA1の通信速度で決まり、38400なら交渉が先、それ以外なら再同期が先です。
順序を逆にすると、どちらかの場合で失敗します。

## pcsc-liteのクライアントプロトコル（`pkg/pcscd`）

### バージョンと構造体

対応するのはバージョン4.6です。
クライアントのマイナーバージョンが4から6までなら受け付け、サーバーのバージョンを返して続行します。
それ以外は`SCARD_E_SERVICE_STOPPED`を返し、クライアントライブラリは続行せずに「デーモンが使えない」と報告します。
マイナーバージョン5以下のクライアントには、そのバージョンが使う固定長配列でリーダーの状態を返します。

構造体は`winscard_msg.h`と`readers.h`に合わせ、サイズをテストで固定しています。
プロトコルが変わったときに最初に失敗するのはこのテストです。
通信の形式はC構造体をホストのメモリー上の配置のまま送るもので、バイト順もホストのものです。
これはクライアントライブラリが送ってくる形式に合わせたものです。

### カードの有無を確認するタイミング

カードが入っているかは、デバイスを開かないと分かりません。
また、デバイスを開くと、受信中のチューナーと同じUSBデバイスを使うことになります。
そのため、確認はクライアントが必要としているときだけ行います。

| 状況 | クライアントが読む状態 |
| --- | --- |
| クライアントが`SCardGetStatusChange`で待っている | 実際に確認する。待っている間はデバイスを開いたままにする（問い合わせのたびに開閉すると、毎回ファームウェアを書き込むことになるため）|
| カードに接続している | `SCardStatus`のたびに実際に確認する |
| どちらでもない | 最後に確認した値。一度も確認していなければ`SCARD_UNKNOWN` |

カードを検出したときに返す状態は`PRESENT | POWERED | NEGOTIABLE | SPECIFIC`です。
これらの経路ではATRを終えたカードしか検出できず、それより前の段階が無いためです。

デバイスを開く必要はありますが、使うハンドルは`coaxd`が持っているものと同じなので、録画中でも開き直しは起きません。
また、最後の利用から`--board-linger`の間はデバイスを開いたままにするので、開閉が続いても開き直しは減ります。

## `fakecardd`での動作確認

`pkg/pcscd/testdata/fakecardd`は、テスト用のスロットを1つ提供するだけのデーモンです。
実機なしで、`libpcsclite`との動作を確認できます。

```sh
CGO_ENABLED=0 go build -o fakecardd ./pkg/pcscd/testdata/fakecardd
docker run -d --name pcsctest debian:stable-slim sleep 3600
docker cp fakecardd pcsctest:/usr/local/bin/
docker exec pcsctest apt-get update
docker exec pcsctest apt-get install -y pcsc-tools
docker exec pcsctest sh -c 'fakecardd /run/pcscd.comm & \
  PCSCLITE_CSOCK_NAME=/run/pcscd.comm pcsc_scan -n'
```

`pcsc_scan`がリーダーを一覧表示し、カードを検出してATRを表示すれば成功です。
libpcsclite 2.3.3とpcsc-tools 1.7.3で、`scriptor`によるAPDUの送受信まで確認しています。
