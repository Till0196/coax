# チューナーの一覧と選局（`coax list`、`coax tune`）

接続されたチューナーの一覧表示と、1チャンネルの受信方法を説明します。
給電や利得の指定は[tune-options.md](tune-options.md)、スキャンは[scan.md](../scanner/scan.md)にあります。

## チューナーを探す（`coax list`）

`list`は、利用できるチューナーを名前付きで一覧表示します。
USBチューナー、Linux DVBのアダプター、Windows BDAのチューナーは、`coaxd`が同じ形式にそろえて1つの一覧にします。
デバイスは開かないので、他のドライバーが使っているデバイスにも影響しません。

```sh
./coax list
```

```
TUNER                        DELIVERY          STATE   MODEL
px4:ABC12345@1-2.1:0         ISDB-S            free    PLEX PX-W3U4 (RT710)
px4:ABC12345@1-2.1:1         ISDB-S            in use  PLEX PX-W3U4 (RT710)
px4:ABC12345@1-2.1:2         ISDB-T            free    PLEX PX-W3U4 (R850)
px4:ABC12345@1-2.1:3         ISDB-T            free    PLEX PX-W3U4 (R850)
dvb:adapter0.frontend0:0     DVB-T, DVB-T2     free    Sony CXD2856
```

チューナー名は、設定ファイルに書ける形になっています。
シリアル番号や物理ポート経路から作るので、再起動や同じポートへの挿し直しでは変わりません。
これらが取得できずbus:addressから作った場合は、抜き差しで変わる旨の注意が付きます（[tuner-host.md](tuner-host.md#チューナーid)）。

チューナーの番号は連番とは限りません。
PX-W3U4のように衛星用と地上用が混在するデバイスでは種別ごとに番号が分かれているので、表示された名前をそのまま使ってください。

Linuxでは、PCIeのチューナーだけでなく、Linux DVBのドライバーが`/dev/dvb`に出すUSBドングルも、同じ`dvb`バックエンドで使えます。

`--tuners`を付けると、各デバイスを実際に開いて構成を確認します（ファームウェアも書き込まれます）。
`--json`を付けるとJSONで出力します。

## 1チャンネルを受信する（`coax tune`）

`tune`は選局してTSを書き出します。
書き出し先は`-o`（`--output`）で指定します。
省略すると標準出力に出力するので、Mirakurunの`command:`にそのまま書けます。

```sh
# 地上デジタル27chを録る
./coax tune --system ISDBT --ch 27 -o ter27.ts

# --ch は日本のテレビチャンネル名を取ります。UHF 13〜62、VHF 1〜12、そして
# ケーブルの中間帯とスーパーハイバンド C13〜C63。13 と C13 は別のチャンネルです。
./coax tune --system ISDBT --ch C23 -o catv23.ts

# 衛星（周波数とStream IDで指定）
./coax tune --system ISDBS --freq 1049480 --stream-id 16625 -o bs.ts

# --ch は衛星チャンネルも取ります。書き方は2通りあり、どちらも通ります。
# recpt1式（トランスポンダと相対ストリーム）
./coax tune --ch BS15_1 -o bs.ts
# Stream ID（日本向けDVB定義ファイルのセクション名、スキャン結果もこちら）
./coax tune --ch 16625 -o bs.ts

# チューナーを指名する
./coax tune --tuner px4:ABC12345@1-2.1:2 --system ISDBT --ch 27 -o ter27.ts

# 外から給電しているので、チューナーからは給電しない
./coax tune --system ISDBS --freq 1049480 --stream-id 16625 --lnb-power off -o bs.ts

# DVB-S2（トランスポンダ名で指定。詳細は ../scanner/dvb-s2.md）
./coax tune --ch JCSAT3A/JD5 -o dvb-s2.ts

# 別のプログラムへ渡すときは -o を省く（-o - と同じ）
./coax tune --system ISDBT --ch 27 | ts-filter > ter27.ts
```

止めるには、`Ctrl-C`かSIGTERMを送ります。
接続が閉じるとチューナーは解放されるので、終了処理は不要です。

チャンネル定義ファイルに書ける項目は、どれでも`-P NAME=VALUE`で渡せます（`-P MODULATION=QAM/64`など）。
書式は[channel-files.md](channel-files.md)と同じです。

`--priority`と`--no-sync`を含む選局オプションは[tune-options.md](tune-options.md)にあります。
