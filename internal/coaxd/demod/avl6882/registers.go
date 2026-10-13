// Copyright (c) 2015 Luis Alves; TurboSight; 2026 Till0196
// SPDX-License-Identifier: GPL-2.0-or-later

package avl6882

// familyAVL68xx is what register 0x40000 answers; anything else is refused.
const familyAVL68xx = 0x68624955

// coreReadyWord is what the booted firmware posts at rsCoreReadyWord.
const coreReadyWord = 0x5aa57ff7

// Clock PLL registers.
const (
	regPLLReset        = 0x00100000
	regPLLDLLInit      = 0x00100008
	regPLLDeglitchMode = 0x00100010

	regPLLSysDivR  = 0x00100048
	regPLLSysDivF  = 0x0010004c
	regPLLSysDivQ  = 0x00100050
	regPLLSysRange = 0x00100054

	regPLLMpegDivR  = 0x00100064
	regPLLMpegDivF  = 0x00100068
	regPLLMpegDivQ  = 0x0010006c
	regPLLMpegRange = 0x00100070

	regPLLADCDivR  = 0x00100080
	regPLLADCDivF  = 0x00100084
	regPLLADCDivQ  = 0x00100088
	regPLLADCRange = 0x0010008c

	regPLLSysEnable2 = 0x001000b4
	regPLLSysEnable3 = 0x001000b8
	regPLLSysDivQ2   = 0x001000bc
	regPLLSysDivQ3   = 0x001000c0

	regPLLMpegEnable2 = 0x001000c4
	regPLLMpegEnable3 = 0x001000c8
	regPLLMpegDivQ2   = 0x001000cc
	regPLLMpegDivQ3   = 0x001000d0

	regPLLADCEnable2 = 0x001000d4
	regPLLADCEnable3 = 0x001000d8
	regPLLADCDivQ2   = 0x001000dc
	regPLLADCDivQ3   = 0x001000e0

	regPLLDLLOutPhase  = 0x00000100
	regPLLDLLReadPhase = 0x00000104
)

// Transport stream output.
const (
	regTSOutput    = 0x00130420
	tsOutputEnable = 0x00000000
	regGPIOBase    = 0x00120000
	gpioAGCDVBTC   = 0x00 // agc1_sel
	gpioAGCDVBS    = 0x10 // agc2_sel
	gpioLNBVoltage = 0x08 // pin 38, lnb_ctrl_1_sel
	gpioLNBPower   = 0x0c // pin 37, lnb_ctrl_0_sel
	gpioLevel0     = 0
	gpioLevel1     = 1
	gpioHighZ      = 2
	gpioAGCOn      = 6
)

// Firmware mailbox commands.
const (
	fwCmdLoadDefault = 1
	fwCmdAcquire     = 2
	fwCmdInitSDRAM   = 17
	fwCmdInitADC     = 18
	fwCmdDMA         = 21
	fwCmdCalcCRC     = 22
	fwCmdPing        = 23
	fwCmdDecompress  = 24
)

// Patch script commands.
const (
	patchCmdValidateCRC     = 0
	patchCmdPing            = 1
	patchCmdLoadToDevice    = 2
	patchCmdDMA             = 3
	patchCmdDecompress      = 4
	patchCmdAssertCPUReset  = 5
	patchCmdReleaseCPUReset = 6
	patchCmdLoadToDeviceImm = 7
	patchCmdReadFromDevice  = 8
	patchCmdDMAHardware     = 9
	patchCmdSetConditionImm = 10
)

// Patch script operand and operator encodings.
const (
	patchCompressionZlib = 0

	patchAddrModeVarIndex = 0

	patchUnaryLogicalNegate = 1
	patchUnaryBitwiseNegate = 2

	patchBinaryLoad       = 0
	patchBinaryAnd        = 1
	patchBinaryOr         = 2
	patchBinaryBitwiseAnd = 3
	patchBinaryBitwiseOr  = 4
	patchBinaryEquals     = 5
	patchBinaryStore      = 6
	patchBinaryNotEquals  = 7

	patchVarArraySize = 32
)

// Common firmware mailbox and status offsets, relative to a per-system base.
const (
	rcFWCommand         = 0x00
	rcFWCommandArgsAddr = 0x58
	rsCurrentActiveMode = 0x24
	rsCoreReadyWord     = 0xa0
	rsRFAGC             = 0x44
)

// Tuner I2C repeater offsets, relative to 0x118000.
const (
	tunerI2CSoftReset    = 0x00
	tunerI2CControl      = 0x04
	tunerI2CBitRptClkDiv = 0x18
	tunerI2CBitRptCntrl  = 0x1c
)

// Transport stream configuration offsets, relative to 0x200.
const (
	rcTSPacketLen     = 0x39
	rcTSContinuous    = 0x3a
	rcTSClockEdge     = 0x3b
	rcTSSerial        = 0x3c
	rcTSSerialMSB     = 0x3e // also rc_ts_packet_order
	rcTSSerialOutPin  = 0x3f
	rcTSValidPolarity = 0x40
	rcTSErrorPolarity = 0x41
	rcTSClockPhase    = 0x96
	rcTSSyncPulse     = 0x97
)

// Error-statistics block offsets, relative to 0x132050.
const (
	esmControl      = 0x04
	esmTickClear    = 0x88
	esmTickType     = 0x8c
	esmTimeTickLow  = 0x90
	esmTimeTickHigh = 0x94
	esmByteTickLow  = 0x98
	esmByteTickHigh = 0x9c
	esmMode         = 0xc0
)

// DiSEqC block offsets, relative to 0x16c000.
const (
	diseqcTxControl  = 0x00
	diseqcToneFracN  = 0x04
	diseqcToneFracD  = 0x08
	diseqcTxStatus   = 0x0c
	diseqcRxMsgTimer = 0x14
	diseqcRxControl  = 0x1c
	diseqcSoftReset  = 0x20
	diseqcSampFracN  = 0x28
	diseqcSampFracD  = 0x2c
	diseqcTxFIFO     = 0x80
)

// DVB-S/S2 configuration offsets, relative to 0xe00, and status offsets
// relative to 0xc00.
const (
	rcDVBSxRFAGCPol      = 0x0000
	rcDVBSxFormat        = 0x0010
	rcDVBSxInput         = 0x0014
	rcDVBSxIntSymRateMHz = 0x0054
	rcDVBSxIntDmdClkMHz  = 0x0164
	rcDVBSxIntMpegClkMHz = 0x0168
	rcDVBSxIntFecClkMHz  = 0x016a
	rcDVBSxFecBypassRate = 0x019a
	rcDVBSxDecodeMode    = 0x0204
	rcDVBSxIQMode        = 0x020a
	rcDVBSxIFOffset10kHz = 0x0234
	rcDVBSxIntADCClkMHz  = 0x02a0

	rsDVBSxIntSNRdB = 0x0020
	rsDVBSxFecLock  = 0x009e
)

// DVB-C configuration offsets, relative to 0x600, and status offsets relative
// to 0x400.
const (
	rcDVBCSymbolRateHz = 0x0000
	rcDVBCSampleRateHz = 0x0004
	rcDVBCDmdClkHz     = 0x0008
	rcDVBCJ83BMode     = 0x0017
	rcDVBCTunerType    = 0x0024
	rcDVBCInputFormat  = 0x0025
	rcDVBCInputSelect  = 0x0027
	rcDVBCIFFreqHz     = 0x0028
	rcDVBCRFAGCPol     = 0x0049
	rcDVBCFecClkHz     = 0x0050
	rcDVBCQAMModeScan  = 0x0090
	rcDVBCADCUsePLLClk = 0x01ee
	rcDVBCADCSel       = 0x01ef

	rsDVBCModeStatus = 0x0004
)

// DVB-T/T2 configuration offsets, relative to 0xa00, and status offsets
// relative to 0x800.
const (
	rcDVBTxFundRateHz      = 0x0004
	rcDVBTxSampleRateHz    = 0x0008
	rcDVBTxRFAGCPol        = 0x000f
	rcDVBTxTunerType       = 0x0040
	rcDVBTxInputFormat     = 0x0041
	rcDVBTxSpectrumInvert  = 0x0042
	rcDVBTxInputSelect     = 0x0043
	rcDVBTxNomCarrierFreq  = 0x0044
	rcDVBTxL1ProcOnly      = 0x0054
	rcDVBTxCommonPLPPresnt = 0x0055
	rcDVBTxCommonPLPID     = 0x0056
	rcDVBTxDataPLPID       = 0x0057
	rcDVBTxLayerSelect     = 0x006a
	rcDVBTxAcquireMode     = 0x006b
	rcDVBTxMpegClkRateHz   = 0x006c
	rcDVBTxADCUsePLLClk    = 0x0076
	rcDVBTxADCSel          = 0x0077

	rsDVBTxFecLock   = 0x00d2
	rsDVBTxSNRdBx100 = 0x00d6
)

// Enumerated configuration values the init sequences write.
const (
	avlOffBinary   = 1 // AVL_OFFBIN
	avlADCIn       = 1 // AVL_ADC_IN
	avlAGCNormal   = 0
	avlAGCInverted = 1
	avlIFQ         = 2 // AVL_IF_Q, the third AVL_ADC_Channel value

	avlDVBCJ83A = 0
	avlDVBCIF   = 0

	avlDVBTxRealIF   = 0
	avlSpectrumAuto  = 2
	avlLockModeTOnly = 4
	avlLockModeAll   = 5

	avlTSParallel            = 0
	avlMPCMRising            = 1
	avlTSContinuousEnable    = 0
	avlMPSPData0             = 0
	avlMPBOMSB               = 1
	avlTSSerialSync1Pulse    = 1
	avlMPEPNormal            = 0
	avlMPVPNormal            = 0
	avlTS188                 = 0
	avlTSParallelOrderNormal = 1
	avlTSParallelPhase0      = 0

	avlErrorStatAuto = 1
	avlErrorStatTime = 1
)

// i2cRepeaterClockDivider is I2C_RPT_DIV, computed from the fixed 250 MHz core
// clock: (0x2A * 250000) / (240 * 1000).
const i2cRepeaterClockDivider = (0x2a * 250000) / (240 * 1000)
