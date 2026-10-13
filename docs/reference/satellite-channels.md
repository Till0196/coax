# 衛星チャンネルの書き方（`BS15_1`か`16625`か）

基本的には意識する必要はありません。
スキャンしたチューナーでそのまま受信するなら、`coax export`が出力した`channel`をそのまま使えます。
スキャンの時点で、そのドライバーが受け付ける形式で書き出されるためです。

例外は、Linuxで別途ビルドするpt3ドライバーとpt1_drvを使う場合です。
この2つは`BS15_1`式しか受け取りません。
`coax export --channel-name transponder`でこの形式に書き出せます。

## 2つの形式の違い

`channels.yml`の`channel`に書いたものは、そのまま`<channel>`へ渡ります。
どちらの形式も受け付けますが、復調器へ渡る値は異なります。

| `channel` | `STREAM_ID`に入る値 | 意味 |
| --- | --- | --- |
| `BS15_1` | `1` | トランスポンダ上の位置。復調器がTMCCから該当ストリームを探します |
| `16625` | `16625` | Stream IDそのもの。Linux DVB APIの`DTV_STREAM_ID`が求める形式です |

Stream IDは位置を含むので（`16625` = `0x40F1` → トランスポンダ15、位置1）、Stream IDから位置は求められます。
逆は求められません。
どの位置にどのストリームが載っているかは、TMCCにしか書かれていないためです。
指定できる場合は、Stream IDのほうが多くの情報を持ちます。

## ドライバー別の対応

指定できる形式は、チューナーではなくドライバーで決まります。
同じPT3でも、別途ビルドするpt3ドライバーとカーネル同梱の`earth-pt3`とで異なります。

| チューナーの掴まれ方 | `STREAM_ID`に書ける値 |
| --- | --- |
| px4_drv（`chardev`バックエンド）| 両方。12未満は位置、12以上はStream ID |
| pt3ドライバーとpt1_drv（同じく`chardev`、Linuxで別途ビルド）| 位置0〜7のみ |
| カーネル同梱の`earth-pt3`、`earth-pt1`（`dvb`バックエンド）| Stream IDのみ |
| その他のLinux DVBドライバー（`dvb`バックエンド）| Stream IDのみ |
| WindowsのPT1/PT2/PT3（`pt3`と`pt1`バックエンド）| 両方。px4_drvと同じ規則です |

制限があるのはpt3ドライバーとpt1_drvだけです。
この2つは、渡された値をTMCCから読んだ8つのストリームの番号としてのみ扱い、範囲も検査しません。
Stream IDを渡すと範囲外を読むため、`coaxd`が8以上の値をエラーにします（[chardev.md](../boards/chardev.md#stream_idの解釈)）。

## 制限のあるドライバーにStream IDを渡したとき

受信を始める前に、理由付きのエラーになります。
別の局が録画されることはありません。

```
the PT3 driver takes STREAM_ID as a relative stream number of 0..7,
not as a stream identifier; 16625 is out of that range
```

チューナーがどちらの形式を受け付けるかは、`GET /tune`の`relative_streams`で分かります（位置のみを受け付ける場合はテーブルの要素数が入り、Stream IDを受け付ける場合は含まれません）。
`coax scan --discover`もこの値を参照して、NITから得たStream IDを位置に置き換えるかどうかを決めています。

なお、カーネル同梱の`earth-pt3`を使う場合、VHF 1〜12とCATV C13〜C63は選局できません（カーネルの`tc90522`が470〜770 MHzしか申告しないため）。
理由と、それを直すパッチは[chardev.md](../boards/chardev.md#カーネル組み込みdvbドライバーという選択肢pt1--pt2--pt3)にあります。
