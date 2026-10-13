// Copyright (c) STMicroelectronics (Mathias Hilaire, Thierry Delahaye); CrazyCat; TurboSight; 2026 Till0196
// SPDX-License-Identifier: GPL-2.0-only

package stid135

// The STiD135 is a system-on-chip rather than a flat register map: each block
// appears once per signal path at its own base. The bases below assume a chip
// base address of zero, and are spelled out rather than computed because the
// steps between paths are not uniform.

// demodBase is D1..D8_DVBSX_DEMOD_ADDR, the demodulator core of each path.
var demodBase = [8]uint16{0x0900, 0x1100, 0x1900, 0x2100, 0x2900, 0x3100, 0x3900, 0x4100}

// viterbiBase is V1..V8_DVBSX_VITERBI_ADDR, the DVB-S and DSS inner decoder.
var viterbiBase = [8]uint16{0x0D00, 0x1500, 0x1D00, 0x2500, 0x2D00, 0x3500, 0x3D00, 0x4500}

// hwareBase is W1..W8_DVBSX_HWARE_ADDR: transport stream output and the error
// counters.
var hwareBase = [8]uint16{0x0E00, 0x1600, 0x1E00, 0x2600, 0x2E00, 0x3600, 0x3E00, 0x4600}

// pktdelinBase is K1..K8_DVBSX_PKTDELIN_ADDR: the DVB-S2 baseband frame
// delineator and the multistream filter in it.
var pktdelinBase = [8]uint16{0x0F00, 0x1700, 0x1F00, 0x2700, 0x2F00, 0x3700, 0x3F00, 0x4700}

// fecspyBase is B1..B8_DVBSX_FECSPY_ADDR.
var fecspyBase = [8]uint16{0x0F80, 0x1780, 0x1F80, 0x2780, 0x2F80, 0x3780, 0x3F80, 0x4780}

// agcrfBase is A1..A4_DVBSX_AGCRF_ADDR: one per RF input, not per demodulator,
// since the gain control sits in front of the shared ADC.
var agcrfBase = [4]uint16{0x0200, 0x0220, 0x0240, 0x0260}

// diseqcBase is D1..D4_DVBSX_DISEQC_ADDR, again one per RF input: a message
// goes out on an aerial lead, not on a demodulator.
var diseqcBase = [4]uint16{0x0300, 0x0340, 0x0380, 0x03C0}

const (
	// rfmuxBase is the crossbar: each demodulator's RF input.
	rfmuxBase = 0x02A0
	// afeBase is the analogue front end, including the PLL every frequency
	// derives from.
	afeBase = 0x9000
)

// Registers of the DVBSX_DEMOD block, as offsets from demodBase.
const (
	regDEMOD       = 0x020
	regDMDCFGMD    = 0x022
	regDMDISTATE   = 0x024
	regDMDMODCOD   = 0x030
	regDMDSTATE    = 0x032
	regDMDFLYW     = 0x033
	regDSTATUS     = 0x034
	regDSTATUS2    = 0x035
	regDSTATUS5    = 0x038
	regDSTATUS6    = 0x039
	regHDEBITCFG2  = 0x040
	regSLICEMAX    = 0x059
	regSLICEMIN    = 0x05a
	regSLICESEL    = 0x05b
	regCORRELMANT  = 0x080
	regCORRELABS   = 0x081
	regCORRELCOEF  = 0x084
	regCORRELCFG   = 0x085
	regCORRELFREQ  = 0x086
	regCARCFG      = 0x0b0
	regCARFREQ     = 0x0b5
	regCARHDR      = 0x0b6
	regCFR1BCFG    = 0x0ba
	regCFRINIT2    = 0x0c3
	regCFRUP2      = 0x0c6
	regCFRLOW2     = 0x0c9
	regCFR12       = 0x0cf
	regCFR11       = 0x0d0
	regCFR10       = 0x0d1
	regSFRINIT2    = 0x0f0
	regSFR3        = 0x0fc
	regSFR2        = 0x0fd
	regSFR1        = 0x0fe
	regSFR0        = 0x0ff
	regTMGREG2     = 0x100
	regTMGREG1     = 0x101
	regTMGREG0     = 0x102
	regTMGOBS      = 0x105
	regDFECFG      = 0x110
	regXFECFG2     = 0x113
	regPEGALCFG    = 0x114
	regCFR2CFR1    = 0x161
	regACLC2S2Q    = 0x169
	regACLC2S28    = 0x16a
	regACLC2S216A  = 0x16b
	regACLC2S232A  = 0x16c
	regACLC2S264A  = 0x16d
	regACLC2S2128A = 0x16e
	regACLC2S2256A = 0x16f
	regNNOSDATAT1  = 0x1a0
	regNNOSPLHT1   = 0x1a4
	regPLSMSCRAMBB = 0x1dc
	regPLROOT2     = 0x1ec
	regPLROOT1     = 0x1ed
	regPLROOT0     = 0x1ee
)

// Field masks of the DVBSX_DEMOD block.
const (
	fldROLLOFFCONTROL   = 0x07 // DEMOD
	fldMANUALSXROLLOFF  = 0x08 // DEMOD
	fldSPECINVCONTROL   = 0x30 // DEMOD
	fldDVBS1ENABLE      = 0x40 // DMDCFGMD
	fldDVBS2ENABLE      = 0x80 // DMDCFGMD
	fldHEADERMODE       = 0x60 // DMDSTATE
	fldFLYWHEELCPT      = 0x0f // DMDFLYW
	fldLOCKDEFINITIF    = 0x08 // DSTATUS
	fldDEMODDELOCK      = 0x80 // DSTATUS2
	fldSHDBERROR        = 0x02 // DSTATUS5
	fldSHDBOVERFLOW     = 0x04 // DSTATUS5
	fldSPECINVDEMOD     = 0x01 // DSTATUS6
	fldSIGNALANNEXEM    = 0xc0 // DSTATUS6
	fldDEMODFLTXXXMODE  = 0x1f // HDEBITCFG2
	fldMODEHAUTDEBIT    = 0xc0 // HDEBITCFG2
	fldROLLOFFSTATUS    = 0xe0 // TMGOBS
	fldPLSCRAMBROOTHigh = 0x03 // PLROOT2
	fldPLSCRAMBMODE     = 0x0c // PLROOT2
)

// Registers and field masks of the DVBSX_VITERBI block.
const (
	regFECM       = 0x03
	regPRVIT      = 0x0a
	regVITCURPUN  = 0x10
	regVSTATUSVIT = 0x14

	fldIQINV     = 0x01 // FECM
	fldDSSDVB    = 0x80 // FECM
	fldVITCURPUN = 0x1f // VITCURPUN
	fldLOCKEDVIT = 0x08 // VSTATUSVIT
)

// Registers and field masks of the DVBSX_HWARE block.
const (
	regTSSTATE1  = 0x00
	regTSCFG2    = 0x02
	regTSCFG0    = 0x04
	regTSDIVN    = 0x06
	regTSPCRPID1 = 0x0b
	regTSDLYSET2 = 0x16
	regTSDLYSET1 = 0x17
	regTSDLYSET0 = 0x18
	regTSSTATUS  = 0x26
	regTSSTATUS2 = 0x27
	regTSDEBUG0  = 0x51
	regERRCTRL1  = 0x60
	regERRCTRL2  = 0x64
	regERRCNT20  = 0x67
	regSFDLYSET2 = 0xc6

	fldTSOUTNOSYNC      = 0x01 // TSSTATE1
	fldRSTHWARE         = 0x01 // TSCFG2
	fldTSFIFOEMBINDVB   = 0x04 // TSCFG0
	fldBYTEOVERSAMPLING = 0x38 // TSDIVN
	fldSOFFIFOPCRADJ    = 0x80 // TSPCRPID1
	fldTSFIFOERROR      = 0x40 // TSSTATUS
	fldTSFIFOLINEOK     = 0x80 // TSSTATUS
	fldTSFIFOLINENOK    = 0x08 // TSSTATUS2
	fldDILNOSYNC        = 0x20 // TSSTATUS2
	fldTSFIFOERROREVNT  = 0x04 // TSDEBUG0
)

// Registers and field masks of the DVBSX_PKTDELIN block.
const (
	regPDELCTRL0   = 0x00
	regPDELCTRL1   = 0x01
	regPDELCTRL2   = 0x02
	regBBHCTRL2    = 0x06
	regISIENTRY    = 0x08
	regISIBITENA   = 0x09
	regPDELSTATUS1 = 0x30

	fldHEMMODESELECT  = 0x03 // PDELCTRL0
	fldALGOSWRST      = 0x01 // PDELCTRL1
	fldFILTEREN       = 0x20 // PDELCTRL1
	fldRESETUPKOCOUNT = 0x40 // PDELCTRL2
	fldFORCEMATYPEMSB = 0x20 // BBHCTRL2
	fldFIRSTLOCK      = 0x01 // PDELSTATUS1
	fldPKTDELINLOCK   = 0x02 // PDELSTATUS1
	fldBCHERRORFLAG   = 0x08 // PDELSTATUS1
	fldPKTDELINDELOCK = 0x80 // PDELSTATUS1
)

// regFBERCPT4 is the DVBSX_FECSPY total packet counter.
const regFBERCPT4 = 0x10

// Registers and field masks of the DVBSX_AGCRF block.
const (
	regAGCRFPOWERI = 0x0a
	regAGCRFIN1    = 0x0c
)

// Registers and field masks of the DVBSX_DISEQC block.
const (
	regDISTXCFG    = 0x02
	regDISTXSTATUS = 0x03
	regDISTXFIFO   = 0x05
	regDISRXCFG    = 0x0a

	fldDISEQCMODE   = 0x03 // DISTXCFG
	fldDISPRECHARGE = 0x04 // DISTXCFG
	fldTXIDLE       = 0x20 // DISTXSTATUS
	fldTXFIFOFULL   = 0x40 // DISTXSTATUS
	fldDISRXON      = 0x01 // DISRXCFG
)

// fldRFMUXDEMODSEL is the two-bit field in each RFMUX register. RFMUXn selects
// the input of demodulator n+1, so the offset is the zero-based index.
const fldRFMUXDEMODSEL = 0x03

// Registers and field masks of the AFE_CTRL block: only the PLL and the
// identification register, all this driver has business touching.
const (
	regAFEPLLREFDIV = 0x01
	regAFEPLLINT    = 0x02
	regAFEPLLFRACH  = 0x03
	regAFEPLLFRACL  = 0x04
	regAFEID        = 0x80

	fldPLLREFDIVN = 0x02 // AFE_PLL_REFDIV
	fldPLLINT     = 0xff // AFE_PLL_INT
	fldPLLSDON    = 0x80 // AFE_PLL_FRAC_H
	fldPLLFRACH   = 0x7f // AFE_PLL_FRAC_H
	fldPLLFRACL   = 0xff // AFE_PLL_FRAC_L
	fldCUTID      = 0x0c // AFE_ID
)
