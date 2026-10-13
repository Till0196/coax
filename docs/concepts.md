# コンセプト

coaxの本体は、チューナーを持つデーモン`coaxd`です。
`coaxd`は状態を持たない作りになっていて、Mirakurunなどから呼ばれる`coax`のCLI、`ZeroWave`、TVTestやEDCBの`BonDriver_coax`が、どれも同じ`coaxd`を使えるようにしています。

```
Mirakurun ──(command:)── coax tune ─┐
ZeroWave ───────────────────────────┼── HTTP ── coaxd ── チューナー
TVTest / EDCB ── BonDriver_coax ────┘
```

## coaxdが持たないもの

`coaxd`は、デバイスを制御してストリームを取り出したり、スマートカードリーダーにアクセスする機能しか持ちません。

- **設定ファイルを持ちません。** 繋がっているチューナーは起動のたびに探索を行います。
- **チャンネル情報を持ちません。** チャンネル一覧はクライアント側（Mirakurunの`channels.yml`、BonDriverのチャンネル設定、`ZeroWave`）が持ち、選局のたびに伝送方式と周波数などをすべて`coaxd`に渡します。
- **セッションを持ちません。** チューナーの割り当てはHTTPの接続に結びついていて、接続が閉じるとそのチューナーは解放されます。ログインや解放のための手続きはありません。

そのため、クライアントは事前の設定なしに`coaxd`を使い始められ、クライアントが異常終了しても`coaxd`側に状態は残りません。
`coaxd`は接続が無い状態が続くと終了し（既定15秒）、必要になったときに`coax`、`BonDriver_coax`、`ZeroWave`が起動します。
**`coaxd`はクライアントと同じフォルダに置いておくだけでよく、基本的には常駐設定は不要です。**
Mirakurunのように常時使う環境では、`--idle 0`で常駐させることもできます。

## なぜデーモンが要るのか

1枚のボードに複数のチューナーとカードリーダーが載っていても、1つのデバイスを開けるのは1つのプロセスだけです。
録画コマンドがそれぞれデバイスを開く構成では、1本目の録画が始まると、2本目の録画もカードリーダーへのアクセスもできなくなります。

`coaxd`はデバイスを1度だけ開き、搭載されたチューナーを接続ごとに割り当てます。
これにより、PX-W3U4の4つのチューナーとカードリーダーを、Mirakurunの録画とTVTestの視聴で同時に使えます。

チューナーに空きが無いときは、要求に付いた優先度で割り当てを決めます。
空きがある間は既存の接続を止めず、空きが無いときは、要求より低い優先度の接続からだけチューナーを取り上げます（[優先度](reference/tuner-host.md#優先度x-coax-priority)）。

## クライアントごとの使い方

| クライアント | `coaxd`との繋がり方 |
| --- | --- |
| Mirakurun | `tuners.yml`の`command:`に`coax tune`を書きます。`coax tune`が`coaxd`に選局を要求し、TSを標準出力に出力します。Mirakurunが`coax tune`を終了すると接続が閉じ、チューナーは解放されます（[mirakurun.md](setup/mirakurun.md)）|
| TVTest、EDCB | `BonDriver_coax.dll`が`coaxd`に直接接続します。チャンネルを切り替えるときは、同じ接続のまま再選局します |
| `ZeroWave` | `coaxd`に直接接続します。`coaxd`が起動していなければ起動します |
| 自作のプログラム | HTTPで`/stream?system=ISDBT&freq=557142857`のようにリクエストすると、レスポンスのボディがTSになります（[tuner-host.md](reference/tuner-host.md)）|

同じマシン上のクライアントとは、Unixドメインソケット（Windowsを含む）で通信します。
別のマシンから使うときは、`--http`でTCPのポートを開きます。

## チューナーへの経路（バックエンド）

`coaxd`がチューナーを制御する経路を、バックエンドと呼びます。
チューナー名の先頭がバックエンド名で、`px4:ABC12345@1-2.1:2`は`px4`バックエンドのチューナーです。

- 直接USB：coaxが個別に対応したデバイス（`px4`、`px3`、`t230`など）は、メーカーのドライバーを使わずに直接制御します。Linux、Windows、macOSで同じように動作します。
- OSのドライバー経由：それ以外のデバイスは、OSにインストールされたドライバーを使います。Linuxなら`dvb`（Linux DVB）や`chardev`（px4_drv、pt3ドライバー）、Windowsなら`bda`です。

`coaxd`はすべてのバックエンドで同時に探索を行い、見つかったチューナーを1つの一覧にまとめます。
クライアントはバックエンドを意識せず、伝送方式と周波数を指定します。
どのデバイスがどのバックエンドになるかは[backends.md](setup/backends.md)にあります。

## 受け取るもの

`coaxd`は、チューナーが出力したストリームをそのまま返します。
地上波、BS、110度CS、ケーブルならTS、高度BSと高度CS（ISDB-S3）ならTLVです。
チューナー内蔵のカードリーダーはPC/SCとして公開するので、外のプログラムに通すときはそれを使います（[card-reader.md](reference/card-reader.md)）。

ストリームを返す前に、TSまたはTLVとして同期が取れているかを検査します。
アンテナが繋がっていなくても復調器がロックを報告することがあり、そのままではノイズを録画してしまうためです（[同期検査](reference/tuner-host.md#同期検査)）。

## 選局に渡すもの

クライアントが`coaxd`に渡すのは、チャンネル定義ファイル（DVBv5形式）と同じ項目です。

| 項目 | 内容 |
| --- | --- |
| 伝送方式（`DELIVERY_SYSTEM`）| `ISDBT`（地上波）、`ISDBS`（BS、110度CS）、`ISDBS3`（高度BS、高度CS）、`ISDBC`（ケーブル）、`DVBS2`（124度、128度のCS）など |
| 周波数（`FREQUENCY`）| 搬送波の位置 |
| `STREAM_ID` | 1つの搬送波に複数のストリームが多重されているとき、取り出すストリーム。BSのTSIDなど |

`coax tune`では、これを`--ch 27`や`--ch BS15_1`のようなチャンネル名で指定できます。
項目の詳細は[channel-files.md](reference/channel-files.md)にあります。
ケーブルの方式（ISDB-C、DVB-C、J.83 Annex B）の関係は[scan.md](scanner/scan.md#ケーブルj83のannexと定義ファイル)に、衛星チャンネルの書き方は[satellite-channels.md](reference/satellite-channels.md)にあります。
