# Mirakurunから使う

Mirakurunの`tuners.yml`の`command:`に`coax tune`を書きます。
`coax`と`coaxd`を同じフォルダに置いておけば、`coax tune`が必要なときに`coaxd`を起動します。
チューナーの確保と解放は接続の開閉で行われるので、ほかの設定は不要です。

```yaml
- name: PX4-T1
  types:
    - GR
  command: coax tune --system ISDBT --ch <channel>
  decoder: ts-filter

- name: PX4-S1
  types:
    - BS
    - CS
  command: coax tune --system ISDBS --ch <channel>
  decoder: ts-filter
```

`--system`は、`--ch`に指定したチャンネル名からも決まります。
物理チャンネル番号ならISDB-T、`BS15_1`や`16625`、`ND02`のような衛星チャンネル名ならISDB-Sになるので、上の2つはどちらも`--system`を省略できます。
`--system`が必要なのは、物理チャンネル番号をケーブルのISDB-Cとして受信するときと、ISDB-S3（MMT/TLV）のときです。
`coax export`が`BS4K`のtypeで書き出したチャンネルには、`--system ISDBS3`を付けてください。
`--system ISDBS`のままだと、TLVではなくISDB-Sとして選局を要求します。

チューナーを固定したいときは、`--tuner`に`coax list`に表示された名前を指定します。
省略すると、Mirakurunの要求ごとに空いているチューナーが選ばれます。

`decoder`には、TSを通す外のプログラムを書きます（ここでは`ts-filter`）。
そのプログラムには、カードリーダーのソケットの場所を指定する必要があります。
`tuners.yml`にはチューナーごとの環境変数が無いので、Mirakurunのプロセス全体に`PCSCLITE_CSOCK_NAME`を渡してください。
このソケットを作るのは`coax pcsc`です。
`coax pcsc`を常駐させておくか、`command:`の`coax tune`に`--pcsc`を付けて必要なときだけ起動するかのどちらかです。
後者は、使われなくなると終了します。
ソケットの場所は、どちらも起動時に表示されます。

衛星チャンネルを`BS15_1`と`16625`のどちらで書くかは[satellite-channels.md](../reference/satellite-channels.md)に、DVB-S2の放送（`szap-s2j`からの移行、`commandVars.satellite`）は[dvb-s2.md](../scanner/dvb-s2.md)にあります。
