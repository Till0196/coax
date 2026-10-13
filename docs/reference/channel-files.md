# 定義ファイルの書き方（共通）

coaxはDVBv5形式のチャンネル定義ファイルを読みます。
`dvbv5-scan`などが書き出したファイルをそのまま使えます。
定義ファイルに書ける項目は、すべてこの文書に記載しています。
HTTPで選局を要求する方法は[tuner-host.md](tuner-host.md)にあります。

## 書式と値の決まり

セクション名には任意の文字列を書けます。
`--include`と`--exclude`の正規表現は、この名前に対して照合します。

```ini
[サンプル-27]
	DELIVERY_SYSTEM = ISDBT
	FREQUENCY = 557142857
```

- 値を囲む`"`と`'`は取り除きます。スキャン結果をそのまま貼り付けられるようにするためです。
- 数値は10進数のほか、`0x`を付けた16進数も読みます。
- 下の表に無い大文字のキー（`SERVICE_ID`、`VIDEO_PID`など）は選局に使わないので無視します。無視したキーは`/tune`の`ignored`と`/stream`の`X-Coax-Ignored`で返します。

## 読むキーの一覧

読むキーは次の21個です。

| キー | 意味 | 書ける値 | 使うバックエンド | 対応していないとき |
| --- | --- | --- | --- | --- |
| `DELIVERY_SYSTEM` | 伝送方式。必須 | 表記の揺れを許容する | すべて | `409 unsupported` |
| `FREQUENCY` | 周波数。必須 | Hz／kHz（後述）| すべて | `409 unsupported`（範囲外）|
| `BANDWIDTH_HZ` | 帯域幅 | Hz | 地上、ケーブル系 | 方式から既定値 |
| `SYMBOL_RATE` | シンボルレート | シンボル/秒 | 衛星、ケーブル系 | - |
| `ROLLOFF` | DVB-S2のロールオフ | `20`／`25`／`35` | 帯域算出と`dvb`／`bda` | 35%とみなす |
| `MODULATION` | 変調方式 | 区切りと数字の位置は問わない（後述）| `dvb`、`bda`、`t230`、`tbs5520se` | 無視 |
| `INVERSION` | スペクトル反転 | DVBv5の値 | `dvb` | 無視 |
| `INNER_FEC` | 内符号 | DVBv5の値 | `dvb`、`bda` | 無視 |
| `CODE_RATE_HP` | 階層伝送の高優先側符号化率 | DVBv5の値 | `dvb` | 無視 |
| `CODE_RATE_LP` | 同、低優先側 | DVBv5の値 | `dvb` | 無視 |
| `TRANSMISSION_MODE` | キャリア数 | DVBv5の値 | `dvb` | 無視 |
| `GUARD_INTERVAL` | ガードインターバル | DVBv5の値 | `dvb` | 無視 |
| `HIERARCHY` | 階層伝送 | DVBv5の値 | `dvb` | 無視 |
| `PILOT` | パイロット信号 | DVBv5の値 | `dvb` | 無視 |
| `STREAM_ID` | 搬送波内のストリームの指定 | 方式ごとに意味が異なる | 衛星系とDVB-T2 | ISDB-Sは相対スロット0、DVB-T2とDVB-S2は復調器が選択 |
| `SCRAMBLING_SEQUENCE_INDEX` | DVB-S2の物理層スクランブル系列 | 0〜262143 | `dvb`、`tbs5520se` | 0（既定の系列） |
| `POLARIZATION` | 偏波 | DVBv5の4つ、1文字の略記、円偏波の2つ | 衛星系 | PLEX系は直線偏波をエラーにする |
| `VOLTAGE` | LNB給電の段 | `off`／`low`／`high`と別名 | 衛星系 | `409 unsupported`（`lnb_supply`）|
| `TONE` | 22kHzトーン | `ON`かどうかだけを判定する | `dvb`、`bda`、TBS系 | PLEX系はエラー、ほかは無視 |
| `SAT_NUMBER` | DiSEqCスイッチのポート | `0`〜`3` | `dvb`、`bda` | 送らない |
| `LNB` | アンテナのLNBfの種類 | 名前の一覧を参照 | すべて（周波数の換算）| - |
| `GAIN`／`LNA` | フロントエンドの利得 | `auto`／`min`／`max`など | デバイスごと | `409 unsupported`（`gain`）|

必須なのは`DELIVERY_SYSTEM`と`FREQUENCY`だけで、ほかは省略しても選局を行います。
表の「無視」は、そのバックエンドでは指定しても使わないという意味です。

## `DELIVERY_SYSTEM`

大文字小文字と、ハイフン、アンダースコア、空白の有無は区別しません。
`ISDB-T`、`ISDBT`、`isdb_t`はすべて同じ方式として扱います。
`DVB-C`だけの場合はAnnex A、`ISDB-C`はAnnex Cとして扱います。
Annex Bは別の方式なので、ほかのどれとも同一視しません。

## `FREQUENCY`

HzでもkHzでも書けます。
1000万以上ならHz、それより小さければkHzとして読みます。
地上波の定義ファイルはHz、衛星はkHzで書くのが一般的で、どちらもそのまま使えます。

衛星の周波数は、指定が無ければ第1中間周波数として読みます（BS-1なら`1049480`）。
ダウンリンク周波数で書く場合は、`LNB`キーでLNBfの種類を指定します。

`LNB`を指定したときだけ、HzとkHzの境界が10億になります。
ダウンリンク周波数はkHzで書いても1200万台になり、1000万を境界にするとすべてHzと判定されるためです。

## `MODULATION`の綴り

区切りの有無と、数字が前か後ろかは問いません。
`QAM/256`、`QAM256`、`qam-256`は同じで、`PSK/8`と`8PSK`も同じです。

書き方が2通り普及しているためです。
カーネルの`fe_modulation`は数字を前に書き（`8PSK`、`16APSK`、`8VSB`）、libdvbv5が書き出すファイルは後ろに書きます（`PSK/8`、`APSK/16`、`VSB/8`）。
`dvbv5-scan`や`w_scan`で作った定義ファイルは後者なので、両方に対応しないと他のツールで作ったファイルを読めません。
coaxに同梱している124/128度CSのチャンネル一覧も`PSK/8`と書いています。

指定できる変調方式は`QPSK`、`QAM/16`、`QAM/32`、`QAM/64`、`QAM/128`、`QAM/256`、`QAM/AUTO`、`PSK/8`、`APSK/16`、`APSK/32`、`VSB/8`、`VSB/16`、`DQPSK`です。
これ以外の値は無視せず、エラーにします。

## `BANDWIDTH_HZ`、`SYMBOL_RATE`、`ROLLOFF`

`BANDWIDTH_HZ`があればそれを使います。
無ければ次のように決めます。

1. 伝送方式で幅が決まっているもの（ISDB-C、ITU-T J.83 Annex B、ATSC）は6MHz
2. それ以外は、`SYMBOL_RATE`とロールオフから計算します

ロールオフの係数はLinux DVBのコアと同じです。

| 伝送方式 | 占有帯域／シンボルレート |
| --- | --- |
| `DVB-C`（＝Annex A）、`DVB-C/ANNEX_A` | 115% |
| `DVB-C/ANNEX_C`（＝ISDB-C）| 113% |
| `DSS` | 120% |
| `DVB-S`、`TURBO`、`ISDB-S` | 135% |
| `DVB-S2` | `ROLLOFF`から（`20`→120%、`25`→125%、`35`→135%）|

DVB-S2で`ROLLOFF`が無いか、上の3つ以外の値の場合は、135%とみなします。
どちらの方法でも帯域幅が決まらなければエラーです。

## `STREAM_ID`

伝送方式によって意味が異なります。

| 伝送方式 | `STREAM_ID`の意味 | 範囲 |
| --- | --- | --- |
| ISDB-S | 相対TSを指すTSID | 0〜65535 |
| ISDB-S3（高度BS/CS）| TLVストリームID | 0〜65535 |
| DVB-T2 | PLP ID | 0〜255 |
| DVB-S2 | 入力ストリーム識別子 | 0〜255 |

DVB-T2とDVB-S2では、省略と0は異なります。
省略すると復調器が選択します（PLP 0を指定したことにはなりません）。

ISDB-Sでは、省略すると相対スロット0を選びます。
復調器を直接制御するバックエンド（`px3`、`px4`、`pxmlt`、`isdb2056`、`us3pout`、`chardev`、Windowsの`pt3`／`pt1`）がこの扱いです。
復調器に選択させないのは、TC90522系は前回選んだTSを保持し、ソニー系（CXD2856）はTSを自動選択しないためです。
どちらも、省略したときの結果が直前の選局によって変わります。
トランスポンダの周波数だけで選局すれば、スロット0のTSからNITを読めます。

DVB-T2とDVB-S2では、識別子として使うのは下位8ビットだけです。
256以上を指定してもエラーにはせず、下位8ビットだけを使います。
カーネルのドライバー（`si2168`、`cxd2820r`）と同じ扱いなので、同じファイルなら`dvbv5-zap`と同じストリームを選びます。

DVB-S2では、上位ビットに物理層スクランブル系列を格納した書き方にも対応しています。
DVB-S2のマルチストリームの定義ファイルで、広く使われている書き方です。

| ビット | 内容 |
| --- | --- |
| 0-7 | 入力ストリーム識別子（ISI） |
| 8-25 | スクランブル系列（18ビット） |
| 26-27 | モード。0なら系列そのもの、それ以外ならgold符号の索引 |

この書き方の値は、[`SCRAMBLING_SEQUENCE_INDEX`](#scrambling_sequence_index)を指定した場合と同じに扱います。
両方を指定したときは`STREAM_ID`のほうを使います。

TC90522系の復調器を直接制御するバックエンド（`chardev`と、Windowsの`pt3`／`pt1`）では、値の解釈が2通りあります。
12未満ならTMCCの相対スロット番号、12以上ならTSIDです。
ISDB-Sの相対スロットは0〜11だけなので、両者が重なることはありません。

### ISDB-S3の書き方

Linux DVB APIにはISDB-S3の伝送方式の番号が無いので、ISDB-S3のチャンネルは`DELIVERY_SYSTEM = ISDBS`のまま、`STREAM_ID`にTLVストリームIDを書きます。
ISDB-SかISDB-S3かは復調器が判別するので、`STREAM_ID`の指定だけで十分です。

そのため`coaxd`も、定義ファイルからはTSかTLVかを判断できません。
`/stream`の[同期検査](tuner-host.md#同期検査)とストリーム統計は、伝送方式の名前ではなくバイト列からTSとTLVを判別します。

```ini
[サンプル-S3]
	DELIVERY_SYSTEM = ISDBS
	FREQUENCY = 1164560
	STREAM_ID = 45168
```

`ISDBS3`と書いても、Linux DVBのフロントエンドへは`SYS_ISDBS`として渡します。
APIにISDB-Sの番号しか無いためです。
1つの復調器が両方を受信し、どちらを受信するかは`STREAM_ID`で決まります。
Windowsのバックエンドは`ISDBS3`をそのまま扱うので、`ISDBS3`と書いたチャンネルはLinuxでもWindowsでもそのまま選局できます。
`coax export --format dvbv5`だけは、libdvbv5に番号が無いので`ISDBS`と書き出します。

## `SCRAMBLING_SEQUENCE_INDEX`

DVB-S2の物理層スクランブル系列の番号です。
18ビット（0〜262143）で、既定は0です。
省略すると0として扱い、エラーにはしません。

放送側がこの番号を必ず通知する仕組みは、規格にはありません。
S2 satellite delivery system descriptorがあればそこから読めますが、無ければ受信側で指定する必要があります。

`STREAM_ID`の上位ビットに格納することもでき（[`STREAM_ID`](#stream_id)）、どちらで指定しても同じです。

使えるのは`dvb`と`tbs5520se`だけです。
`dvb`はドライバーへそのまま渡し、`tbs5520se`はSi2183にgold符号の初期値として書き込みます。
ほかのバックエンドはこの設定を持たないので、指定しても使いません。

## 同軸ケーブルへの出力

`POLARIZATION`、`VOLTAGE`、`TONE`、`SAT_NUMBER`の4つは、復調器の設定ではなく、同軸ケーブルに出力する電圧や信号の指定です。

### `POLARIZATION`

DVBv5の4つ（`HORIZONTAL`、`VERTICAL`、`LEFT`、`RIGHT`）、ほかのツールが書く1文字の略記（`H`、`V`、`L`、`R`）、`CIRCULAR_LEFT`と`CIRCULAR_RIGHT`を読みます。
`OFF`は偏波を指定しないという意味で、エラーにはなりません。

`H`と`L`は高いほうの電圧、`V`と`R`は低いほうの電圧になります。

### `VOLTAGE`

LNBへの給電を、電圧の値ではなく段で表します。

| 書き方 | 意味 |
| --- | --- |
| `off`、`0`、`none` | 給電しない |
| `low`、`11`、`13` | 低いほう |
| `high`、`on`、`15`、`18` | 高いほう |

段で表すのは、出力できる電圧がハードウェアごとに異なるためです。
数値も段の別名として指定できますが、その電圧が出力されるとは限りません。

省略すると給電しません。
recpt1、recdvb、recisdbと同じ既定です。
給電が必要な配線で給電を忘れても、録画できないだけで済みます。
しかし、電源挿入器やブースターがすでに給電している配線にさらに給電すると、機器を損傷するおそれがあります。
給電を忘れた場合は、[同期検査](tuner-host.md#同期検査)が`no_sync`ですぐに選局を失敗させるので、検出できます。

ただし`POLARIZATION`があれば、省略とはみなしません。
DVB-Sのチャンネルファイルは必ず偏波を書くので、その偏波に対応する電圧を出力します。
給電しないのは、電圧も偏波も指定していないときだけです。

### `TONE`

値は`ON`かどうかだけを判定します。
バンドの切り替えが必要なLNBf（`UNIVERSAL`など）では、切り替え周波数より上で自動的にトーンを出力しますが、チャンネルに`TONE`があればその指定を優先します。

### `SAT_NUMBER`（DiSEqC）

DiSEqCスイッチのポート番号で、`0`〜`3`の4つです。
選局の直前に、DiSEqC 1.0のポート切り替えのコマンド（フレーミングバイト、全スイッチ宛てのアドレス、ポートグループ0への書き込み）を送信します。
同じメッセージにはポート番号のほか、トーンで選ぶバンドと電圧で選ぶ偏波も含めるので、その2つは`TONE`と`VOLTAGE`／`POLARIZATION`から決めます。

- `SAT_NUMBER`が無いチャンネルでは何も送りません。
- 衛星以外の伝送方式で指定するとエラーです。
- `0`〜`3`以外もエラーです（スイッチは4ポートです）。
- 送信できるのは`dvb`と`bda`だけです。ほかのバックエンドは無視します。
- 給電しない（`VOLTAGE = off`）ときは、コマンドを重畳する電源が無いので送りません。

### バックエンドごとの対応

| バックエンド | `low` | `high` | `off` | 22kHzトーン |
| --- | --- | --- | --- | --- |
| `dvb` | 13V | 18V | できる | ○ |
| `bda` | ドライバー任せ | ドライバー任せ | できない | ○（`LNB`指定時）|
| `dd`（Windows PCIe）| 13V | 18V | できる | ○ |
| `tbs`（Windows PCIe）| 13V | 18V | できる（TAS2101の6902／6904／6910はできない。quattro給電の6909／6909Xと6812の2本目は給電を操作しない）| ○（復調ICのSECブロック経由。[tbs.md](../boards/tbs.md)）|
| `tbs5520`、`tbs5530` | 13V | 18V | できる | ○ |
| `tbs5520se` | 13V | 18V | できない | ○（Si2183のDiSEqCコマンド経由）|
| `chardev`、Windowsの`pt3`／`pt1` | 11V | 15V | できる | − |
| `px4`、`pxmlt`、`isdb2056`、`px3` | 給電する | 給電する | できる | エラー |

PLEX系のUSBチューナー（`px4`／`pxmlt`／`isdb2056`／`px3`）は、GPIOで1系統の給電を入切するだけで段が無いので、`low`も`high`も「給電する」になります。
給電はデバイスの全チューナーで共有なので、`off`は「そのチューナーとしては給電を要求しない」という意味になります。
すべてのチューナーが`off`なら給電しませんが、ほかのチューナーが給電を使っている間は切りません。

`off`に対応しないバックエンドは、`off`を明示したときだけエラーにします。
指定が無ければ、その配線には常に給電されているとみなして選局を続けます。

### `POLARIZATION`との優先順位

DVB-SやTBS系では、電圧がそのまま偏波の選択になります。
この場合は`POLARIZATION`のほうを使います（偏波を間違えると受信できませんが、段の違いは受信に影響しないためです）。
ただし`off`は`POLARIZATION`より優先します。
Windowsの`pt3`／`pt1`も同じ規則です。

PLEX系のUSBチューナーは、直線偏波（`H`／`V`）を指定するとエラーにします。
給電の入切しかできず偏波を選べないので、別の偏波で受信してしまうことを防ぐためです。
`POLARIZATION`を参照しないのは、Linuxの`chardev`バックエンドだけです。

### 11V／15Vの扱い

`chardev`は`PTX_ENABLE_LNB_POWER`で11V／15Vを出力します（WindowsのPT1/PT2/PT3も同じ2段です）。
Linux DVB API（`dvb`バックエンド）には13V／18V／OFFしか無く、11V／15Vは指定できません。
海外製のDVB-S2チューナーを日本の124度／128度CSのアンテナに接続するときは、日本向けの製品か変換器が必要です。

### libdvbv5と同じファイルを使う場合

`VOLTAGE`には`13`／`18`／`OFF`だけを書いてください。
libdvbv5が`VOLTAGE`に受け付けるのはこの3つだけで、ほかの値があるとファイル全体が構文エラーになります（未知の*キー*は無視されますが、既知のキーの未知の*値*はエラーです）。
`low`や`high`と書いたファイルは、`coaxd`では読めても`dvbv5-zap`では読めません。

なお、libdvbv5は選局のときに`VOLTAGE`を使いません。
読み込みはしますが、`DTV_VOLTAGE`はどの伝送方式のプロパティ一覧にも含まれておらず、`dvb_fe_store_parm()`が`command VOLTAGE (25) not found during store`を出力して終わります。
libdvbv5で電圧を決めるのは`POLARIZATION`とLNBfです。
`coaxd`は`VOLTAGE`を指定どおりに適用します。

## `LNB`とダウンリンク周波数

衛星の`FREQUENCY`には2通りの書き方があり、どちらで書いたかを`LNB`キーで区別します。

| `LNB`の有無 | `FREQUENCY`の意味 | 例 |
| --- | --- | --- |
| 無し | 第1中間周波数（kHz）。そのままチューナーへ渡します | `1068000` |
| 有り | ダウンリンク周波数（kHz）。LNBfの局部発振周波数を引いてから渡します | `12268000` |

`LNB`を指定しない限り、周波数は換算しません。
一般に配布されているISDB-Sの定義ファイルは中間周波数を書いていて`LNB`が無いので、そのまま使えます。

キーの名前も値の名前もlibdvbv5と同じなので、`dvbv5-scan`が書き出した`LNB = UNIVERSAL`もそのまま使えます。

| 名前 | 内容 | ダウンリンク | 局部発振 |
| --- | --- | --- | --- |
| `UNIVERSAL` | ヨーロッパのユニバーサル | 10800〜12700MHz | 9750 / 10600MHz（11700MHzで切替）|
| `EXTENDED` | 拡張ユニバーサル | 10700〜12750MHz | 9750 / 10600MHz（同上）|
| `ENHANCED` | 単一バンドKu | 10700〜11700MHz | 9750MHz |
| `STANDARD` | 標準Ku | 10945〜11450MHz | 10000MHz |
| `DBS` | 北米 | 12200〜12700MHz | 11250MHz |
| `C-BAND` | Cバンド | 3700〜4200MHz | 5150MHz |
| `110BS` | 日本 BS / CS-110 | 11710〜12751MHz | 10678MHz |
| `JCSAT3A` | 日本 128度CS | 12200〜12750MHz | 11200MHz、トーンOFF |
| `JCSAT4B`（旧名`JCSAT4A`も可）| 日本 124度CS | 12200〜12750MHz | 11200MHz、トーンON |

日本の124/128度CSは2つの衛星を1本の同軸にまとめていて、22kHzトーンでどちらの衛星を受信するかを選びます。
局部発振周波数はどちらも11200MHzなので、周波数だけではどちらか決まりません。
そのため名前を分けています。
ダウンリンクの範囲は、実際に使われているトランスポンダの上端・下端ではなく、ブロックの帯域にしています。

124度に名前が2つあるのは、衛星が交代したためです。
この軌道位置の衛星は、2012年まではJCSAT-4A、それ以降はJCSAT-4Bです。
これらの定義の元になったszap-s2jは`JCSAT4A`と書くので、表示は`JCSAT4B`にし、`JCSAT4A`も受け付けます。
どちらで書いたチャンネルファイルも選局でき、`LNB`キーは`JCSAT4B`にそろえてバックエンドへ渡します。
ブロック自体は変わっていません。
局部発振周波数、同軸、トーンも同じです。

局部発振周波数の11200MHzは、市販の日本向けCSコンバーター各社の仕様や、BonDriver_BDAの既定値と同じです。
偏波は直線偏波（水平／垂直）で、BS/110度CSの円偏波とは異なります。
衛星を切り替える信号は32〜53kHzとされていますが、DVBのハードウェアが出力するのは22kHzです。
22kHzで日本のCSコンバーターが確実に切り替わるかは、実機では確認していません。

ダウンリンクの範囲から外れた周波数は、換算せずエラーにします。
中間周波数を`LNB`付きで渡した場合がこれにあたり、そのまま局部発振周波数を引くと、実在しない周波数になるためです。

偏波を1本にまとめる方式（バンドスタッキング）のLNBfには対応していません。
局部発振周波数の選択に偏波が必要なうえ、coaxが対応するハードウェアにそうした配線のものが無いためです。

## `GAIN`と`LNA`

`GAIN`は、チューナー入力段の増幅の設定です。
省略するとハードウェアの起動時の設定のままです。

| 値 | 意味 |
| --- | --- |
| `auto` | ドライバーに任せる。Linuxなら`LNA_AUTO`、それ以外は起動時のまま |
| `min`（`off`／`low`）／`max`（`on`／`high`）| そのハードウェアで設定できる範囲の両端 |
| `+13dB`／`-9.9dB` | 単位付きの値。ハードウェアが持つ最も近い設定に合わせ、選んだ値を返します |

単位の無い数値は受け付けません。
段の番号はデバイスによって意味が異なるためです。
Linux DVB APIは`1`を「入」と解釈し、FC0012の24段の設定は`1`を「ほぼ最小の利得」と解釈するので、同じファイルがデバイスによって正反対の意味になります。
デバイスによらず同じ意味になる単位はデシベルだけです。

増幅器が入切だけで、その利得が公開されていないデバイスでは、`dB`での指定をエラーにします（`min`／`max`／`auto`で指定してください）。
どちらかの端に丸めると、実際とは異なる利得を報告することになるためです。

選ばれた値は`coax list`（`/tuners`）の`settings`に表示されます。
`+13dB`と指定しても`+13.5dB`が選ばれることがあるので、実際の値を確認できるようにしています。

`LNA`キーも同じ設定として読みます（`off`は`min`、`on`は`max`、`auto`）。
libdvbv5と同じファイルを使えるようにするためです。
`GAIN`と`LNA`を両方指定するとエラーになります。

| バックエンド | 段 | 取れる値 |
| --- | --- | --- |
| `dvb`（Linux）| `DTV_LNA` | `min`／`max`／`auto`。`dB`は不可（APIに利得の値の概念がありません）。増幅器を持たないフロントエンドではドライバーが無視します |
| `pxmlt`（CXD2858ER）| 外部増幅器 | 入切のみ。`dB`は不可。起動時は両入力とも入 |
| `px3`のうちPX-S3U／S3U2／W3U2／W3U3（FC0012）| 内蔵LNA | −9.9dB〜+19.7dBの24段。起動時は最上段 |
| `px3`のうち外部増幅器を持つ個体 | 外部増幅器 | 入切のみ |
| `px4`／`isdb2056`の衛星側（RT710／RT720）| IF段のFineGain | 0／1／2／3dB（RT720は0／3dBの2段）。起動時は3dB |
| 上記以外 | - | エラーにします。無視はしません |

`px4`／`isdb2056`の地上側（R850）には設定がありません。
LNA、RF、ミキサーの利得はどれも周波数帯ごとに決まったテーブルから選ばれ、外部から変更する方法がありません。

## libdvbv5との違い

`LNB`を指定すればダウンリンク周波数のファイルも使えますが、`LNB`が無いときの解釈はlibdvbv5と逆です。

| | libdvbv5（`dvbv5-zap`）| coaxd |
| --- | --- | --- |
| `LNB`／`--lnbf`が無いとき | 衛星は選局できない（`Need a LNBf to work`。ISDB-Sは`110BS`を補完する）| 中間周波数としてそのまま使う |
| `LNB`があるとき | ダウンリンクとして局部発振周波数を引く | 同じ |

日本のISDB-Sのドライバーに合わせているためです。
カーネルのISDB-Sフロントエンド（`tc90522`＋`qm1d1c0042`）が`DTV_FREQUENCY`に期待するのは950000〜2151000kHzの中間周波数で、一般に配布されている定義ファイルもBS-1を`FREQUENCY = 1049480`と書いています。
この組み合わせでは局部発振周波数を引かないのが正しいので、`LNB`が無いときは引きません。

その結果、

- 日本のISDB-S定義ファイル（中間周波数で記載）はcoaxdで使え、`dvbv5-zap`では使えません（`110BS`の範囲外としてエラーになります）。
- `dvbv5-scan`が出力したDVB-S定義ファイルは、`LNB`キーがあればcoaxdでも使えます。無ければ`-P LNB=UNIVERSAL`か`coax tune --lnbf`で補ってください。

`LNB`（LNBfの種類）と`lnb_power`（給電）は別の設定です。
前者は局部発振周波数の指定、後者は同軸に給電するかどうかの指定です。

## バックエンドごとに必要なプロパティ

| バックエンド | 伝送方式 | 必要なプロパティ |
| --- | --- | --- |
| [`t230`](../boards/t230.md) | DVB-T、DVB-T2、DVB-C、DVB-C/ANNEX_B、DVB-C/ANNEX_C、ISDB-C | `FREQUENCY`。DVB-T/T2は`BANDWIDTH_HZ`、Annex A/Cは`SYMBOL_RATE`、Annex Bは`MODULATION` |
| [`px3`](../boards/px3.md) | ISDB-T、ISDB-S | `FREQUENCY`。ISDB-Sは`STREAM_ID`（省略時は相対スロット0） |
| [`px4`](../boards/px4.md) | ISDB-T、ISDB-S | `FREQUENCY`。ISDB-Sは`STREAM_ID`（省略時は相対スロット0） |
| [`pxmlt`](../boards/px4.md) | ISDB-T、ISDB-S、ISDB-C、DVB-T、DVB-T2、DVB-C/ANNEX_A、DVB-C/ANNEX_B、DVB-S、DVB-S2 | `FREQUENCY`。ISDB-Sは`STREAM_ID`（省略時は相対スロット0）、DVB-T2は`STREAM_ID`をPLP IDとして解釈、Annex Bは`SYMBOL_RATE` |
| [`isdb2056`](../boards/px4.md) | ISDB-T、ISDB-S | `FREQUENCY`。ISDB-Sは`STREAM_ID`（省略時は相対スロット0） |
| [`s1ur`](../boards/px4.md) | ISDB-T | `FREQUENCY` |
| [`us3pout`](../boards/us3pout.md) | ISDB-T、ISDB-S | `FREQUENCY`。ISDB-Sは`STREAM_ID`（省略時は相対スロット0） |
| [`fsusb2`](../boards/fsusb2.md) | ISDB-T | `FREQUENCY` |
| [`fsusb2i`](../boards/fsusb2i.md) | ISDB-T | `FREQUENCY` |
| [`s270`](../boards/s270.md) | ISDB-T、DVB-T | `FREQUENCY`。DVB-Tは`BANDWIDTH_HZ`（6/7/8MHz）|
| [`tbs5520`](../boards/tbs.md) | DVB-T、DVB-T2、DVB-C、DVB-S、DVB-S2 | `FREQUENCY`。衛星は`SYMBOL_RATE` |
| [`tbs5520se`](../boards/tbs.md) | DVB-T、DVB-T2、DVB-C、DVB-C/ANNEX_B、ISDB-T、DVB-S、DVB-S2 | 同上 |
| [`tbs5530`](../boards/tbs.md) | DVB-T、DVB-T2、DVB-C、DVB-C/ANNEX_B、ISDB-T、ATSC、DVB-S、DVB-S2 | 同上 |
| [`chardev`](../boards/chardev.md) | ISDB-T、ISDB-S | `FREQUENCY`。ISDB-Sは`STREAM_ID`（省略時は相対TS番号0）と`VOLTAGE`。ドライバーのチャンネルテーブルにある周波数だけ |
| [`chardev`](../boards/chardev.md)（Linux、pt3ドライバー）| ISDB-T、ISDB-S | 同上。ただし`STREAM_ID`はTSIDではなく0〜7の相対番号、地上波の周波数補正なし、UHF 62ch不可 |
| [`chardev`](../boards/chardev.md)（Linux、pt1_drv）| ISDB-T、ISDB-S | 同上。ただしUHF 62chとCS奇数ND01〜ND23も選局できます |
| [`pt3`／`pt1`](../boards/earthsoft-windows.md)（Windows）| ISDB-T、ISDB-S | `FREQUENCY`は周波数のまま指定。`STREAM_ID`はTSIDも12未満の相対スロットも可（省略時は相対スロット0）|
| [`dd`](../boards/dd.md)（Windows PCIe）| ボードごと | 伝送方式に応じて。衛星は`SYMBOL_RATE` |
| [`tbs`](../boards/tbs.md)（Windows PCIe）| ISDB-T、ISDB-S、ISDB-S3、DVB-T、DVB-T2、DVB-C、DVB-S、DVB-S2、J.83 Annex B | `FREQUENCY`。衛星は`SYMBOL_RATE`必須。デバイスにより対応方式は異なります |
| [`dvb`](../boards/dvb.md)（Linux DVB）| Linux DVB APIで表現できるもの全般（ISDB-S3を除く）| 伝送方式に応じて |
| [`bda`](../boards/bda.md)（Windows）| Windows BDAが対応するもの | 伝送方式に応じて |

`coax list --tuners`を使うと、チューナーごとに受信できる伝送方式が表示されます。
1台で衛星用と地上用のチューナーが分かれているデバイス（`px3`、`px4`など）では、こちらで確認するほうが確実です。
