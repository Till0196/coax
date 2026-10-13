# ファームウェアの用意

基本的には`coaxd`と同じフォルダに`firmware`フォルダを作り、決まったファイル名でファームウェアファイルを入れるだけです。

```
coaxd(.exe)
coax(.exe)
firmware/
  it930x-firmware.bin
  dvb-demod-si2168-b40-01.fw
  ...
```

ファイル名は下の表のとおりです。
名前の違うファイルは読み込みません。
ファームウェアファイルはOSに依存しないので、同じ`firmware`フォルダをLinux、Windows、macOSで共用できます。
coaxはファームウェアを同梱していないので、ファイルは各自で用意してください。

PT3、US-3POUT、KTV-FSUSB2（EM2874版）や、OSのドライバー経由（`dvb`、`chardev`、`bda`）で使うデバイスは、ファームウェアなしで動作します。

## デバイスごとに必要なファイル

| バックエンド | 主なデバイス | 置くファイル | 入手方法 |
| --- | --- | --- | --- |
| `px4`、`pxmlt`、`isdb2056`、`s1ur` | PX-W3U4、PX-Q3U4、PX-MLT5PE、PX-M1UR、PX-S1URなど | `it930x-firmware.bin` | PLEXのドライバーから[`fwextract`](#fwextract)で取り出す |
| `px3` | PX-S3U、PX-W3U3など | `asv5211-firmware.bin` | 同上 |
| `fsusb2i` | KTV-FSMINI、KTV-FSUSB2/V3 | `it9175-firmware.bin` | 恵安のドライバーから`fwextract`で取り出す |
| `t230` | MyGica T230系 | `dvb-demod-si2168-*.fw`（a20、a30、b40、d60のうちデバイスに合うもの）とチューナー用（[t230.md](../boards/t230.md#ファームウェア)）| [公開ファームウェア集](#公開ファームウェア集から入手できるもの) |
| `s270` | MyGica S270、PX-S1UD | `isdbt_rio.inp`（DVB-Tも受けるなら`dvb_rio.inp`も）| 公開ファームウェア集 |
| `tbs5520` | TBS 5520 | `dvb-usb-tbs5520-01.fw`、`dvb-demod-avl6882.fw` | 公開ファームウェア集かTBSの配布物 |
| `tbs5520se` | TBS 5520SE | `dvb-usb-id5520se.fw`、`dvb-demod-si2183-b60-01.fw`、Si2157用 | 同上 |
| `tbs5530` | TBS 5530 | `dvb-usb-id5530.fw`、`dvb-demod-m88rs6060.fw` | 同上 |
| `fsusb2`、`us3pout`、`cech-zd1j`、`pt3`／`pt1`（Windows）、`dd`、`tbs`（PCIe）、`dvb`、`chardev`、`bda` | | 不要 | |

ファイルが無いと選局は`no_firmware`で失敗し、必要なファイル名と探索したパスがすべてエラーに含まれます（`coax list --tuners`でも確認できます）。

## ほかの探索場所

`coaxd`と同じフォルダの`firmware`のほかに、OSごとに決まったディレクトリも探索します。
Linuxで、すでに`/lib/firmware`に同じファイルがあればそれを使います。

| OS | 探索する場所（上から順）|
| --- | --- |
| Linux | `/lib/firmware`、`/usr/lib/firmware`、`/usr/local/lib/firmware`、`coaxd`の隣 |
| macOS | `/usr/local/lib/firmware`、`/opt/homebrew/lib/firmware`、`coaxd`の隣、`~/Library/Application Support/coaxd/firmware` |
| Windows | `%ProgramData%\coaxd\firmware`、`coaxd`の隣、`%LOCALAPPDATA%\coaxd\firmware` |

表の「`coaxd`の隣」は、`coaxd`と同じフォルダの`firmware`、`coaxd`と同じフォルダの順に探索します。
カレントディレクトリは探索しません。
誰でも書き込めるディレクトリで`coaxd`を起動したときに、そこに置かれたファイルを読み込まないためです。

別の場所に置く場合は、`coaxd --firmware-dir <フォルダ>`で指定します。
指定したフォルダは最初に探索されます。
起動時のオプションなので、すでに起動している`coaxd`には反映されません。

以下の表は2026年8月時点のものです。

## 公開ファームウェア集から入手できるもの

[LibreELEC/dvb-firmware][le]が最も網羅的です。
まずここを確認してください。

| ファイル | バックエンド | [linux-firmware][lf] | [osmc][osmc] | [OpenELEC][oe] | [LibreELEC][le] | [CoreELEC][ce] |
| --- | --- | :-: | :-: | :-: | :-: | :-: |
| `dvb-demod-si2168-a20-01.fw` | `t230` | − | ○ | ○ | ○ | ○ |
| `dvb-demod-si2168-a30-01.fw` | `t230` | − | ○ | ○ | ○ | ○ |
| `dvb-demod-si2168-b40-01.fw` | `t230` | − | ○ | ○ | ○ | ○ |
| `dvb-demod-si2168-d60-01.fw` | `t230` | − | ○ | − | ○ | ○ |
| `dvb-tuner-si2141-a10-01.fw` | `t230` | − | ○ | − | ○ | ○ |
| `dvb-tuner-si2157-a30-01.fw` | `t230` | − | − | − | ○ | ○ |
| `dvb_driver_si2157_rom50.fw` | `t230` | − | − | − | ○ | − |
| `isdbt_rio.inp` | `s270`（ISDB-T）| ○ | ○ | ○ | ○ | ○ |
| `dvb_rio.inp` | `s270`（DVB-T）| − | ○ | ○ | ○ | ○ |
| `dvb-usb-tbs5520-01.fw` | `tbs5520` | − | − | − | ○ | ○ |
| `dvb-demod-avl6882.fw` | `tbs5520` | − | ○ | − | ○ | ○ |
| `dvb-usb-id5520se.fw` | `tbs5520se` | − | ○ | − | ○ | ○ |
| `dvb-demod-si2183-b60-01.fw` | `tbs5520se` | − | ○ | − | ○ | ○ |
| `dvb-usb-id5530.fw` | `tbs5530` | − | − | − | ○ | − |
| `dvb-demod-m88rs6060.fw` | `tbs5530` | − | − | − | ○ | − |

[lf]: https://git.kernel.org/pub/scm/linux/kernel/git/firmware/linux-firmware.git
[osmc]: https://github.com/osmc/dvb-firmware-osmc
[oe]: https://github.com/OpenELEC/dvb-firmware
[le]: https://github.com/LibreELEC/dvb-firmware
[ce]: https://github.com/CoreELEC/dvb-firmware

カーネル公式のlinux-firmwareに入っているのは`isdbt_rio.inp`だけで、同じSianoでもDVB-T用の`dvb_rio.inp`は入っていません。
Silicon LabsのSi2168／Si2141／Si2157系は再配布条件の都合で収録されておらず、上記のコミュニティ製ファームウェア集かメーカー配布のドライバーパッケージから入手してください。

ファイルはリポジトリ直下（osmc）または`firmware/`配下（OpenELEC、LibreELEC、CoreELEC）にあります。

```sh
# 例: LibreELECからT230Cに必要なものを取り、coaxdの隣のfirmwareへ置く
git clone --depth 1 https://github.com/LibreELEC/dvb-firmware.git
mkdir -p firmware
cp dvb-firmware/firmware/dvb-demod-si2168-*.fw \
   dvb-firmware/firmware/dvb-tuner-si214*.fw \
   dvb-firmware/firmware/dvb_driver_si2157_rom50.fw \
   firmware/
```

TBS製品は、メーカーも[tbsdtv.com](https://www.tbsdtv.com/)でファームウェア一式を配布しています。
こちらは1つの`.tar.bz2`に全チップ分がまとまっており、`fwextract`の`--all`ですべて取り出せます。

## 公開ファームウェア集にないもの

次のファイルは上記のどのリポジトリにもありません。
いずれもメーカー配布のドライバーパッケージから`fwextract`で取り出します。

| ファイル | バックエンド | 取得元のパッケージ |
| --- | --- | --- |
| `it930x-firmware.bin` | `px4`、`pxmlt`、`isdb2056`、`s1ur` | PLEX配布のWindowsドライバー（例: `202104_PX-W3U4_Driver.zip`）。[px4_drv](https://github.com/nns779/px4_drv)の`fwtool`でも同じものが取り出せます |
| `it9175-firmware.bin` | `fsusb2i` | 恵安配布のドライバーパッケージ（`digibest_tv_2j997.zip`）|
| `asv5211-firmware.bin` | `px3` | PLEX公式のLinuxドライバー（`PX-SERIES_ver.1.0_Linux_Driver.zip`）|
| `dvb_driver_si2141_rom60.fw`／`rom61.fw` | `t230` | MyGica／Geniatech配布のドライバーパッケージ。`dvb-tuner-si2141-a10-01.fw`へ自動フォールバックするので、この2つ自体は通常不要です。フォールバック先のsi2142向けファームウェアは必ず置いてください（[t230.md](../boards/t230.md#ファームウェア)）|

### 入手先

いずれも実際に取得し、`fwextract`で取り出せることを確認しました。
SHA-256は取り出したあとのファームウェアのものです。

**`it930x-firmware.bin`**（`px4`、`pxmlt`、`isdb2056`、`s1ur`）

PLEXの[ダウンロードページ](https://plex-net.co.jp/download/)に各製品のWindowsドライバーがあります。
`202104_PX-W3U4_Driver.zip`、`202104_PX-MLT5PE_Driver.zip`、`PX-M1UR_Driver.zip`、`PX-S1UR_Driver.zip`の4つを取り出して比べたところ、中身は同一でした（`294a1d673a9d6e8b48a6a46c51da727a9f23df5d5ce6eb2495732f19462698fa`）。
使うデバイスのものをダウンロードすれば十分です。

**`asv5211-firmware.bin`**（`px3`）

配布ページはPLEXのサイトから削除されていますが、ファイルは現在もダウンロードできます。

```sh
curl -O https://plex-net.co.jp/plex/PX-SERIES_ver.1.0_Linux_Driver.zip
./fwextract PX-SERIES_ver.1.0_Linux_Driver.zip   # b45d510200a1690b3ca358d93de13f40e1d3567b663c17e773349ad96f597aa8
```

当時の配布ページはInternet Archiveに残っており、[PX-W3U3のもの](https://web.archive.org/web/20150211005046/http://www.plex-net.co.jp/product/pxw3u3_download.html)と[PX-W3PEのもの](https://web.archive.org/web/20150309023323/http://www.plex-net.co.jp/product/pxw3pe_download.html)のどちらも同じzipを指しています。

同じページにある`PX-W3PE_LinuxDriver_ver.1.0.0.zip`にファームウェアは入っていません。
中身はPCIe側のASV5220用カーネルモジュール（`asv5220_dtv.ko`）で、`px3`が扱うASV5211とは別のチップです。
Windows用の`PX-W3U3_driver_Ver-1.1.zip`にも、このイメージはそのままの形では入っていません。

**`it9175-firmware.bin`**（`fsusb2i`）

恵安のドライバーパッケージから取り出します。
[サポートのダウンロードページ](https://www.keian.co.jp/support/download)でフリーワードに「KTV-FSUSB2/V3 最新版ドライバ・ソフトウェア」と入力すると表示されます。

```sh
curl -O https://www.keian.co.jp/img/2015/07/digibest_tv_2j997.zip   # 約63MiB
./fwextract digibest_tv_2j997.zip   # 274e1240396acb65defaf43bade6a719490b09dd102a5469eaf53c0e9201933c
```

KTV-FSMINIにドライバーの配布はありませんが、同じ`it9175-firmware.bin`で動作します（[fsusb2i.md](../boards/fsusb2i.md)）。

## fwextract

`cmd/fwextract`は、メーカー配布のドライバーパッケージからファームウェアを取り出すツールです。
パッケージを指定すると、決まった名前のファイルをパッケージと同じフォルダに書き出します。
そのファイルを`firmware`フォルダに移してください。
Windowsでは、ダウンロードしたzipを`fwextract.exe`にドラッグ＆ドロップするだけでも取り出せます。

```sh
go build -o fwextract ./cmd/fwextract

# 1つ取り出す
./fwextract digibest_tv_2j997.zip            # it9175-firmware.bin
./fwextract 202104_PX-W3U4_Driver.zip        # it930x-firmware.bin
./fwextract PX-SERIES_ver.1.0_Linux_Driver.zip # asv5211-firmware.bin

# パッケージの中身を全部取り出す
./fwextract --all --output firmware tbs-tuner-firmwares_v1.0.tar.bz2
```

zip、gzip、bzip2、tar、InstallShieldのセットアップランチャー、`.msi`、`.cab`、Windowsの`.sys`、Linuxの`.ko`を受け付けます。
入れ子になったアーカイブは中身から形式を判別するので、どの階層のファイルを渡しても構いません。
どのチップのファームウェアかも中身から判定します。

`--all`はパッケージ内を再帰的に探索し、ドライバーに埋め込まれたイメージと、そのまま置かれている`.fw`の両方を取り出します。
1枚のボードでブリッジ、復調器、チューナーの3つのファームウェアが必要なこともあるため、1つずつ指定するより確実です。

配布元それぞれのライセンスに従ってください。
ファームウェアの再配布可否はチップメーカーごとに異なります。
