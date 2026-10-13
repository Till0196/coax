# アーキテクチャ

`pkg/host`は`coaxd`のクライアントで、`coaxd`の自動起動と、APIのJSONの型の複製を持ちます。
`pkg/tuner`は、スキャナーから見たチューナーの形です。
`coax`と`coaxd`でJSONの形が食い違わないよう、同じ記録済みのJSON（`pkg/host/testdata/wire.json`）を読む同じテストを両方に置いています。

## TS/TLVの処理

- **`pkg/mpegts`**：MPEG-2 TSの伝送層。188バイトのパケット、ペイロードの取り出し、MPEG CRC32、セクションの同一性の判定、PIDごとのセクションの組み立て（`SectionCollector`）を持ちます。テーブルの意味は扱いません
- **`pkg/mmttlv`**：MMT/TLVの伝送層。TLVパケットの境界、IPv4/IPv6、ヘッダー圧縮IP、UDP、MMTPパケットヘッダー、分割・集約されたMMTシグナリングの組み立てを持ちます。こちらもシグナリングの意味は扱いません
- **`pkg/isdb`**：日本固有の層。PSI/SI（PAT、NIT、SDT、サービス記述子）、TLV-NIT、M2メッセージ、MH-SDT、MHサービス記述子、リモコンキー記述子、ARIB STD-B24の文字列、TSMF（52スロット、相対TS/TLVの分離）を読みます。TSかTLVかの判別（`DetectFormat`）と、解析結果のまとめ（`Analyze`）もここです

依存は一方向で、`mpegts`と`mmttlv`を使うのは`isdb`だけです。
伝送層の2つは、互いにも`isdb`にも依存しません。

Rust版のクレートとの対応は次のとおりです。

| Goのパッケージ | Rust側のクレート |
| --- | --- |
| `pkg/mpegts` | `mpegts` |
| `pkg/mmttlv` | `mmttlv` |
| `pkg/isdb` | `isdb` |
| `config`、`plan`、`scanner`、`exporter`、`host` ほか | `coax` |

## チャンネルスキャン用の周波数プラン

周波数プランは`pkg/plan`にあります。
対象は、日本のテレビのチャンネル配置（VHF、ミッドバンド、スーパーハイバンド、UHF）、光配信の周波数グリッド、東経110度のBSとNDのトランスポンダ、東経124度と128度のJCSATのトランスポンダです。
スキャンで周波数を決めるとき、`--ch`の名前から周波数を引くとき、ChSetやch2の物理チャンネル番号を振るときは、どれもこのテーブルを読みます。
物理チャンネル番号はプランの中の並び順そのものなので、番号のテーブルを別には持ちません。

## ネットワーク情報テーブルからの発見

チャンネルを見つける基本の方法は、プランのすべての周波数を順に選局することではなく、1つの多重を選局してNITを読むことです。
その網の残りのチャンネルはNITに書いてあります。
記述子を選局のプロパティに変えるのは`scanner/delivery.go`です。
日本のBSとCSはこの方法で見つけ、プランを使うのは最初の1波を受けるためだけです。

### 衛星記述子の1バイト

`satellite_delivery_system_descriptor`の6バイト目は、上位3ビット（west_east、偏波）が共通で、下位5ビットの割り方だけがARIBとDVBで異なります。

| ビット | ARIB STD-B10 | DVB EN 300 468 |
| --- | --- | --- |
| 4–0 | modulation（5ビット1つ） | roll_off(4–3) / modulation_system(2) / modulation_type(1–0) |

どちらの割り方かを示すビットはありません。
記述子だけでは判断できないので、「何で選局してそのNITを読んだか」で決めます。
NITは自分の網を記述するので、ISDB-Sでロックして読んだNITならARIBの割り方、DVB-S2でロックして読んだならDVBの割り方として扱い、その情報をスキャンの間保持します（`scanner.Convention`）。
軌道位置は手がかりになりません。
124度と128度のCSは、日本の軌道位置にありながらDVB-S2です。

`isdb.SatelliteDelivery`は下位5ビットをARIBの割り方のまま`Modulation`に持ち、DVBの割り方での読みは`DVBSecondGeneration`、`DVBRollOff`、`DVBModulation`が返します。
同じフィールドを別の読み方で読むだけなので、パーサーは分岐しません。

### ダウンリンク周波数の変換をどこでやるか

記述子に書いてあるのはダウンリンク周波数で、チューナーに渡す周波数ではありません。
日本の右旋・左旋は旋回方向で局部発振周波数が決まるので、クライアント側で引き算します（`plan.IntermediateFrequencyKHz`）。
それ以外は、アンテナのLNBの種類を`LNB`に書いて渡し、`coaxd`の`tuning.ApplyLNBf`が周波数の変換とトーンの有無を決めます。
`coaxd`のLNBfテーブルは一般的な種類（Universal、Extended、Enhanced、Standard、DBS、C-band）を持つので、クライアント側はLNBfのテーブルを持ちません。

### ケーブルの場合

`cable_delivery_system_descriptor`は変調に1バイトを丸ごと使うので、衛星のような割り方の違いはありません。
伝送方式は、選局したときのものをそのまま使います。
記述子には伝送方式が書かれておらず、ケーブルは1つの事業者がすべての搬送波で同じ方式を使うからです。

ケーブルでは、NITを読まずにプランだけを使います（`walkedPlans`）。
プランの全波を、日本の事業者がよく使う諸元（ISDB-Cは5.274Msym/s、光配信は5.6Msym/s）で順に選局します。

先にNITを読まないのは、実測した系統では、どの搬送波のNITにもその搬送波自身しか書かれておらず、読んでも選局の回数が増えるだけのためです。
搬送波を並べて書くNITのために、その記述子をプランに重ねる処理（`mergeCarriers`、周波数はプランのまま、ほかの諸元はNITのもの）はありますが、スキャンでは使いません。

## 周辺の役割分担

残りのパッケージの役割は次のとおりです。

- `pkg/config`：DVBv5形式の定義ファイル、伝送方式、チャンネル名
- `pkg/report`：結果のJSONの形
- `pkg/progress`：中断からの再開と進捗の表示
- `pkg/exporter`：JSON、Mirakurunのチャンネル定義、ChSet、TVTestのチャンネル設定、EDCBのチャンネル情報への変換
- `pkg/cli`：サブコマンド
- `pkg/scanner`：`pkg/tuner`を通した選局、ロック待ち、ストリームの受信を繰り返すスキャンのループだけ

`cmd/coax`には`main.go`だけを置いています。
テストは、それぞれの処理を担当するパッケージに置いています。
