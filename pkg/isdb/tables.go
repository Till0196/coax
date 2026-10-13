// Copyright 2026 Till0196
// SPDX-License-Identifier: Apache-2.0 OR GPL-2.0-or-later

package isdb

var alphanumericExceptions = map[byte]rune{
	0x5c: '¥',
	0x7e: '‾',
}

var additionalSymbolSegments = []struct {
	first uint16
	runes string
}{
	{0x7521, "\u3402\U00020158\u4efd\u4eff\u4f9a\u4fc9\u509c\u511e\u51bc\u351f\u5307\u5361"},
	{0x752d, "\u536c\u8a79\U00020bb7\u544d\u5496\u549c\u54a9\u550e\u554a\u5672\u56e4\u5733"},
	{0x7539, "\u5734\ufa10\u5880\u59e4\u5a23\u5a55\u5bec\ufa11\u37e2\u5eac\u5f34\u5f45\u5fb7\u6017"},
	{0x7547, "\ufa6b\u6130\u6624\u66c8\x00\u66fa\u66fb\u6852\u9fc4\u6911\u693b\u6a45\u6a91\x00\U000233cc"},
	{0x7556, "\U000233fe\U000235c4\u6bf1\u6ce0\u6d2e\ufa45\u6dbf\u6dca\u6df8\ufa46\u6f5e"},
	{0x7561, "\u6ff9\u7064\ufa6c\U000242ee\u7147\u71c1\u7200\u739f\u73a8\u73c9\u73d6\u741b\u7421"},
	{0x756e, "\ufa4a\u7426\u742a\u742c\u7439\u744b\u3eda\u7575\u7581\u7772\u4093\u78c8"},
	{0x757a, "\u78e0\x00\x00\u9fc6\u4103"},
	{0x7621, "\u9fc5\u79da\u7a1e\u7b7f\u7c31\u4264\u7d8b\u7fa1\u8118\u813a\ufa6d\u82ae"},
	{0x762e, "\u84dc\x00\u8559\u85ce\x00\u87ec\u880b\u88f5\x00\u8af6\u8dce\x00\u8ff6\u90dd\u9127"},
	{0x763e, "\u91b2\u9233\u9288\u9321\u9348\u9592\u96de\x00\u9940\u9ad9\x00\u9dd7\u9eb4\u9eb5"},
	{0x7a21, "\u26cc\u26cd\u2757\u26cf\u26d0\u26d1\x00\u26d2\u26d5\u26d3\u26d4"},
	{0x7a30, "\U0001f17f\U0001f18a\x00\x00\u26d6\u26d7\u26d8\u26d9\u26da\u26db\u26dc"},
	{0x7a3b, "\u26dd\u26de\u26df\u26e0\u26e1\u2b55\u3248\u3249\u324a\u324b\u324c"},
	{0x7a46, "\u324d\u324e\u324f"},
	{0x7a4d, "\u2491\u2492\u2493\U0001f14a\U0001f14c\U0001f13f\U0001f146\U0001f14b"},
	{0x7a55, "\U0001f210\U0001f211\U0001f212\U0001f213\U0001f142\U0001f214\U0001f215"},
	{0x7a5c, "\U0001f216\U0001f14d\U0001f131\U0001f13d\u2b1b\u2b24\U0001f217\U0001f218"},
	{0x7a64, "\U0001f219\U0001f21a\U0001f21b\u26bf\U0001f21c\U0001f21d\U0001f21e"},
	{0x7a6b, "\U0001f21f\U0001f220\U0001f221\U0001f222\U0001f223\U0001f224\U0001f225"},
	{0x7a72, "\U0001f14e\u3299\U0001f200"},
	{0x7b21, "\u26e3\u2b56\u2b57\u2b58\u2b59\u2613\u328b\x00\u26e8\u3246\u3245\u26e9"},
	{0x7b2d, "\u0fd6\u26ea\u26eb\u26ec\u2668\u26ed\u26ee\u26ef\u2693\u2708\u26f0"},
	{0x7b38, "\u26f1\u26f2\u26f3\u26f4\u26f5\U0001f157\u24b9\u24c8\u26f6\U0001f15f"},
	{0x7b42, "\U0001f18b\U0001f18d\U0001f18c\U0001f179\u26f7\u26f8\u26f9\u26fa\U0001f17b"},
	{0x7b4b, "\u260e\u26fb\u26fc\u26fd\u26fe\U0001f17c\u26ff"},
	{0x7c21, "\u27a1\u2b05\u2b06\u2b07\u2b2f\u2b2e"},
	{0x7c2b, "\u33a1\u33a5\u339d\u33a0\u33a4\U0001f100\u2488\u2489\u248a\u248b\u248c\u248d"},
	{0x7c37, "\u248e\u248f\u2490"},
	{0x7c40, "\U0001f101\U0001f102\U0001f103\U0001f104\U0001f105\U0001f106\U0001f107"},
	{0x7c47, "\U0001f108\U0001f109\U0001f10a\u3233\u3236\u3232\u3231\u3239\u3244\u25b6\u25c0"},
	{0x7c52, "\u3016\u3017\u27d0\u00b2\u00b3\U0001f12d"},
	{0x7c76, "\U0001f12c\U0001f12b\u3247\U0001f190\U0001f226\u213b"},
	{0x7d21, "\u322a\u322b\u322c\u322d\u322e\u322f\u3230\u3237\u337e\u337d\u337c\u337b\u2116\u2121"},
	{0x7d2f, "\u3036\u26be\U0001f240\U0001f241\U0001f242\U0001f243\U0001f244\U0001f245"},
	{0x7d37, "\U0001f246\U0001f247\U0001f248\U0001f12a\U0001f227\U0001f228\U0001f229"},
	{0x7d3f, "\U0001f22a\U0001f22b\U0001f22c\U0001f22d\U0001f22e\U0001f22f\U0001f230"},
	{0x7d46, "\U0001f231\u2113\u338f\u3390\u33ca\u339e\u33a2\u3371\x00\x00\u00bd\u2189"},
	{0x7d52, "\u2153\u2154\u00bc\u00be\u2155\u2156\u2157\u2158\u2159\u215a\u2150"},
	{0x7d5d, "\u215b\u2151\u2152\u2600\u2601\u2602\u26c4\u2616\u2617\u26c9\u26ca"},
	{0x7d68, "\u2666\u2665\u2663\u2660\u26cb\u2a00\u203c\u2049\u26c5\u2614\u26c6"},
	{0x7d73, "\u2603\u26c7\u26a1\u26c8\x00\u269e\u269f\u266c"},
	{0x7e21, "\u2160\u2161\u2162\u2163\u2164\u2165\u2166\u2167\u2168\u2169\u216a\u216b\u2470\u2471\u2472\u2473\u2474\u2475"},
	{0x7e33, "\u2476\u2477\u2478\u2479\u247a\u247b\u247c\u247d\u247e\u247f\u3251"},
	{0x7e3e, "\u3252\u3253\u3254\U0001f110\U0001f111\U0001f112\U0001f113\U0001f114"},
	{0x7e46, "\U0001f115\U0001f116\U0001f117\U0001f118\U0001f119\U0001f11a\U0001f11b"},
	{0x7e4d, "\U0001f11c\U0001f11d\U0001f11e\U0001f11f\U0001f120\U0001f121\U0001f122"},
	{0x7e54, "\U0001f123\U0001f124\U0001f125\U0001f126\U0001f127\U0001f128\U0001f129"},
	{0x7e5b, "\u3255\u3256\u3257\u3258\u3259\u325a\u2460\u2461\u2462\u2463\u2464\u2465\u2466\u2467\u2468\u2469"},
	{0x7e6b, "\u246a\u246b\u246c\u246d\u246e\u246f\u2776\u2777\u2778\u2779\u277a\u277b\u277c\u277d"},
	{0x7e79, "\u277e\u277f\u24eb\u24ec\u325b"},
}

var additionalSymbolCells = func() map[rune]uint16 {
	out := make(map[rune]uint16, 512)
	for _, seg := range additionalSymbolSegments {
		cell := seg.first
		for _, r := range seg.runes {
			if r != 0 {
				out[r] = cell
			}
			cell++
		}
	}
	return out
}()

var additionalDecodeAliases = map[uint16]rune{
	0x754b: '\U000066d9', // 曙
	0x7554: '\U00006adb', // 櫛
	0x757b: '\U00007947', // 祇
	0x757c: '\U000079ae', // 禮
	0x762d: '\U0000845b', // 葛
	0x762f: '\U000084ec', // 蓬
	0x7632: '\U00008755', // 蝕
	0x7636: '\U000089d2', // 角
	0x7639: '\U00008fbb', // 辻
	0x763d: '\U0000912d', // 鄭
	0x7645: '\U00009903', // 餃
	0x7648: '\U00009bd6', // 鯖
	0x7b28: '\U00003012', // 〒
	0x7c27: '\U00005e74', // 年
	0x7c28: '\U00006708', // 月
	0x7c29: '\U000065e5', // 日
	0x7c2a: '\U00005186', // 円
	0x7c3a: '\U0000e290',
	0x7c3b: '\U0000e291',
	0x7c3c: '\U0000e292',
	0x7c3d: '\U0000e293',
	0x7c3e: '\U0000e294',
	0x7c3f: '\U0000e295',
	0x7c58: '\U0000e2a5',
	0x7c59: '\U0000e2a6',
	0x7c5a: '\U0000e2a7',
	0x7c5b: '\U0000e2a8',
	0x7c5c: '\U0000e2a9',
	0x7c5d: '\U0000e2aa',
	0x7c5e: '\U0000e2ab',
	0x7c5f: '\U0000e2ac',
	0x7c60: '\U0000e2ad',
	0x7c61: '\U0000e2ae',
	0x7c62: '\U0000e2af',
	0x7c63: '\U0000e2b0',
	0x7c64: '\U0000e2b1',
	0x7c65: '\U0000e2b2',
	0x7c66: '\U0000e2b3',
	0x7c67: '\U0000e2b4',
	0x7c68: '\U0000e2b5',
	0x7c69: '\U0000e2b6',
	0x7c6a: '\U0000e2b7',
	0x7c6b: '\U0000e2b8',
	0x7c6c: '\U0000e2b9',
	0x7c6d: '\U0000e2ba',
	0x7c6e: '\U0000e2bb',
	0x7c6f: '\U0000e2bc',
	0x7c70: '\U0000e2bd',
	0x7c71: '\U0000e2be',
	0x7c72: '\U0000e2bf',
	0x7c73: '\U0000e2c0',
	0x7c74: '\U0000e2c1',
	0x7c75: '\U0000e2c2',
	0x7d3e: '\U0001f214', // 🈔
	0x7d7b: '\U0000260e', // ☎
}

func init() {
	for r := range additionalSymbolCells {
		if r < 0x80 {
			panic("arib: additional symbol set claims an ASCII scalar")
		}
	}
}
