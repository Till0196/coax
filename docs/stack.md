# 既存のチューナー管理ソフトウェアとの違い

coaxが、Mirakurun、EPGStation、TVTest、EDCB、recpt1とどこで接続するかをまとめます。
`coax`と`coaxd`自身の使い方は[本体README](../README.md)にあります。

Mirakurunもcoaxも、HTTPでストリームを取り出せる点は同じですが、役割が異なります。
Mirakurunが配信するのはサービス単位のストリームで、同じストリームを受信している間は、複数の要求で1つのチューナーを共有します。
番組表の定期的な取得もMirakurunが行います。
`coaxd`が提供するのはチューナー単位のストリームで、1つの接続が1つのチューナーを占有します。
番組表もチャンネル一覧も持たず、選局先は毎回クライアントが指定します。
`coaxd`はMirakurunを置き換えるものではなく、その下で動作するものです。

## Linuxディストリビューション

```mermaid
flowchart TB
  subgraph watch["視聴と録画予約"]
    epgstation["EPGStation"]
  end
  subgraph guide["サービス単位の配信と番組表"]
    mirakurun["Mirakurun（同じストリームならチューナーを共有）"]
  end
  subgraph command["録画コマンド"]
    cli["coax（クライアントCLI）"]
    rec["recpt1 / recdvb / dvbv5-zap"]
  end
  subgraph manage["チューナーの制御と管理"]
    coaxd["coaxd"]
  end
  subgraph kernel["カーネル（OSのドライバー）"]
    usb["usbfs（/dev/bus/usb）"]
    dvb["Linux DVB（/dev/dvb）"]
    chardev["px4_drv / pt3 / pt1"]
  end
  subgraph hardware["ハードウェア"]
    tuner["チューナー"]
  end

  epgstation -->|HTTP| mirakurun
  mirakurun -->|command: coax tune| cli
  mirakurun -.->|command: recpt1| rec
  cli -->|HTTP| coaxd
  coaxd --> usb
  coaxd --> dvb
  coaxd --> chardev
  rec -.-> dvb
  rec -.-> chardev
  usb --> tuner
  dvb --> tuner
  chardev --> tuner
```

Mirakurunから見ると、`coax tune`は`recpt1`や`recdvb`、`dvbv5-zap`と同じ録画コマンドです。
違いは、チューナーを開くのが`coax`ではなく`coaxd`で、1つの`coaxd`がすべてのバックエンドをまとめ、チューナーの割り当ても決めることです。
点線は、`coaxd`を通さない従来の経路です。

## Windows

```mermaid
flowchart TB
  subgraph watch["視聴と録画予約"]
    tvtest["TVTest"]
    edcb["EDCB"]
  end
  subgraph bondriver["BonDriver（アプリから見たチューナー）"]
    bon["BonDriver_coax.dll"]
    others["BonDriver_PT3 / BonDriver_BDA"]
  end
  subgraph manage["チューナーの制御と管理"]
    coaxd["coaxd"]
  end
  subgraph kernel["カーネル（OSのドライバー）"]
    winusb["WinUSB"]
    bda["BDAドライバー（メーカー製）"]
    windriver["WinDriver（レジスタへのアクセスのみ）"]
  end
  subgraph hardware["ハードウェア"]
    tuner["チューナー"]
  end

  tvtest --> bon
  edcb --> bon
  tvtest -.-> others
  edcb -.-> others
  bon -->|HTTP| coaxd
  coaxd --> winusb
  coaxd --> bda
  coaxd --> windriver
  others -.-> bda
  winusb --> tuner
  bda --> tuner
  windriver --> tuner
```

TVTestとEDCBは、`BonDriver_coax`をほかのBonDriverと同じように読み込みます。
Mirakurunが無くても、`coax list`に表示されるチューナーをそのままTVTestやEDCBで使えます。
