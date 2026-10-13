# セットアップガイド

チューナーで1チャンネルを受信するまでの手順を説明します。
必要な作業は、1つのフォルダに`coax`、`coaxd`、`firmware`を置き、`coaxd`がチューナーにアクセスできるようにOSを設定することです。
`coaxd`は設定ファイルを持ちません。

## 1. フォルダを作る

`coax`と`coaxd`を同じフォルダに置きます。
ソースからビルドする場合は、Go 1.25以降で次のコマンドを実行します。

```sh
go build -o coaxd ./cmd/coaxd
go build -o coax  ./cmd/coax
```

Windows向けは`GOOS=windows`、macOS向けは`GOOS=darwin`を指定すれば、どのOSからでもクロスビルドできます（`CGO_ENABLED=0`のまま）。

`coaxd`を手動で起動する必要はありません。
`coax`、`BonDriver_coax`、`ZeroWave`は、同じフォルダの`coaxd`を必要なときに起動します。
接続が無い状態が続くと、`coaxd`は終了します。

## 2. ファームウェアを置く

デバイスによってはファームウェアが要ります。
ファームウェアが必要なデバイスの場合は、同じフォルダに`firmware`フォルダを作り、決まった名前のファイルを入れます。

```
coax
coaxd
firmware/
  it930x-firmware.bin     ← PX-W3U4、PX-MLT5PE、PX-M1URなど
```

デバイスごとに必要なファイルと入手方法は[firmware.md](firmware.md)にあります。
PT3、US-3POUTや、OSのドライバー経由で使うデバイスには不要です。

## 3. OSの設定

coaxが直接制御するUSBチューナーは、`coaxd`からアクセスできるようにOSを設定します。

| OS | 設定 |
| --- | --- |
| Windows | [`driver/windows/`](../../driver/windows)の使うデバイスの`.inf`を`install-winusb.bat`にドラッグします（WinUSBが割り当てられます）|
| Linux | udevルールでUSBデバイスへの書き込み権限を与えます。カーネルのドライバーがバインドしているデバイスはunbindします |
| macOS | 設定は不要です |

詳しくは[os.md](os.md)にあります。

px4_drvやLinux DVB、WindowsのBDAドライバーですでに動作しているチューナーは、そのまま使えます。
`coaxd`がそのドライバーを経由して制御します（[backends.md](backends.md)）。

## 4. 受信の確認

チューナーが認識されているかを確認します。

```sh
./coax list
```

```
TUNER                        DELIVERY          STATE   MODEL
px4:ABC12345@1-2.1:0         ISDB-S            free    PLEX PX-W3U4 (RT710)
px4:ABC12345@1-2.1:2         ISDB-T            free    PLEX PX-W3U4 (R850)
```

1チャンネルを受信します（Ctrl-Cで停止します）。

```sh
# 地上波の27ch
./coax tune --ch 27 > ter27.ts

# BS（アンテナへの給電が要るなら --lnb-power high）
./coax tune --ch BS15_1 > bs.ts
```

### 受信できないとき

エラーには原因を示す理由が付きます。

| エラー | 原因 | 対処 |
| --- | --- | --- |
| 一覧に出ない | WindowsでWinUSBを割り当てていない、Linuxでカーネルのドライバーがバインドしている | [os.md](os.md) |
| `no_access` | USBデバイスへの書き込み権限が無い | udevルールを入れる（[os.md](os.md#linux)）|
| `no_firmware` | ファームウェアが無い。必要なファイル名と探索したパスが表示されます | `firmware`フォルダに置く（[firmware.md](firmware.md)）|
| `tuner_busy` | 空いているチューナーが無い | 他の受信が終わるのを待つ |
| `no_lock` | 時間内にロックしなかった | チャンネル、アンテナを確かめる |
| `no_sync` | ロックしたが、TSまたはTLVとして同期が取れない | アンテナの接続と、衛星なら給電（`--lnb-power`）を確かめる |

エラーの一覧は[tuner-host.md](../reference/tuner-host.md#エラー)にあります。

## 5. 既存のソフトから使う

| ソフト | 設定 |
| --- | --- |
| Mirakurun | `tuners.yml`の`command:`に`coax tune --ch <channel>`を書きます（[mirakurun.md](mirakurun.md)）|
| TVTest、EDCB | `BonDriver_coax.dll`を置きます。`coaxd`はDLLの隣か`PATH`から探します |
| ZeroWave | 受信元に`coaxd`を選びます。`coaxd`はZeroWaveの隣（macOSでは設定ウィンドウの「coaxd のフォルダを開く」で開く場所）に置けば、ZeroWaveが起動します |

Mirakurunのように常時使う環境では、`coaxd --idle 0`で常駐させると、起動の待ち時間がなくなります。
チャンネル一覧は`coax scan`で作り、`coax export`でMirakurunの`channels.yml`などに書き出せます（[scan.md](../scanner/scan.md)、[export.md](../scanner/export.md)）。
