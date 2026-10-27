// Copyright 2026 Till0196
// SPDX-License-Identifier: Apache-2.0 OR GPL-2.0-or-later

package mb86h47

import (
	. "github.com/Till0196/coax/internal/coaxd/usb/bridge/mb86h47/fr30/fc"
)

// core1Stack is where core 1's stack starts, below its variables.
const core1Stack = 0xa37fc

// i2cPolls is how long core 1 waits on the I2C master, in polls.
const i2cPolls = 0x40000

// buildCore1 is core 1's program: the I2C transfers of the mailbox, the ring
// and the command channel (see i2c.go and command.go for what the host puts
// where).
func buildCore1() ([]uint32, error) {
	p := NewProgram(core1Origin)
	main := p.Func("main", 0)
	p.Entry(core1Stack, main)

	word := func(address uint32) *Node { return Mem32(K(address)) }
	vars := func(offset uint32) *Node { return word(commandVars + offset) }
	i2c := func(register uint32) *Node { return Mem32(K(register)) }

	// wait waits for the byte in flight: 0, or the error.
	wait := p.Func("wait", 0)
	wait.Define(func(b *Block) {
		n := b.Tmp("n", i2cPolls)
		b.Loop(func(b *Block) {
			c := b.Tmp("c", i2c(i2cControl))
			b.If(c.And(0x80), func(b *Block) { b.Return(i2cBusError) })
			b.If(c.And(0x01), func(b *Block) { b.Return(i2cOK) })
			b.Inc(n, -1)
			b.If(n.Eq(0), func(b *Block) { b.Return(i2cStuck) })
		})
	})
	// send puts a byte on the bus with the given control (0x50: after a
	// start if the bus is free; 0x70: after a repeated start) and checks its
	// acknowledge: 0, or the error (noAck when not acknowledged).
	send := p.Func("send", 3) // byte, control, noAck
	send.Define(func(b *Block) {
		v, control, noAck := send.Param(0), send.Param(1), send.Param(2)
		b.Set(i2c(i2cData), v)
		b.Set(i2c(i2cControl), control)
		err := b.Tmp("err", wait.Call())
		b.If(err.Ne(i2cOK), func(b *Block) { b.Return(err) })
		b.If(i2c(i2cStatus).And(0x10), func(b *Block) { b.Return(noAck) })
		b.Return(i2cOK)
	})
	// idle waits, within a budget, for the bus to be released.
	idle := p.Func("idle", 0)
	idle.Define(func(b *Block) {
		n := b.Tmp("n", i2cPolls)
		b.While(i2c(i2cStatus).And(0x80).Ne(0), func(b *Block) {
			b.Inc(n, -1)
			b.If(n.Eq(0), func(b *Block) { b.Break() })
		})
	})

	// transfer runs the transfer a mailbox describes (+1 the address, +2
	// the bytes to write, +3 the bytes to read, the bytes to write at +4,
	// those read to +0x84): it writes, if there is anything to write, then
	// reads, after a repeated start. It gives 0 or the error, and after an
	// error leaves the bus stopped.
	run := p.Func("run", 1)
	run.Define(func(b *Block) {
		m := run.Param(0)
		address := b.Tmp("address", Mem8(m.Add(1)))
		writes := b.Tmp("writes", Mem8(m.Add(2)))
		reads := b.Tmp("reads", Mem8(m.Add(3)))
		err := b.Tmp("err", i2cOK)
		b.If(writes.Ne(0), func(b *Block) {
			b.Do(idle.Call())
			b.Set(err, send.Call(address.And(0xfe), 0x50, i2cNoAddressAck))
			b.If(err.Ne(i2cOK), func(b *Block) { b.Return(err) })
			at := b.Tmp("at", m.Add(mailboxWrite))
			b.While(writes.Ne(0), func(b *Block) {
				b.Set(err, send.Call(Mem8(at), 0x50, i2cNoDataAck))
				b.If(err.Ne(i2cOK), func(b *Block) { b.Return(err) })
				b.Inc(at, 1)
				b.Inc(writes, -1)
			})
			b.If(reads.Eq(0), func(b *Block) {
				b.Set(i2c(i2cControl), 0x40)
				b.Do(idle.Call())
				b.Return(i2cOK)
			})
			// A read that follows starts again without a stop.
			b.Set(err, send.Call(address.Or(1), 0x70, i2cNoAddressAck))
		}).Else(func(b *Block) {
			b.If(reads.Eq(0), func(b *Block) { b.Return(i2cOK) })
			b.Do(idle.Call())
			b.Set(err, send.Call(address.Or(1), 0x50, i2cNoAddressAck))
		})
		b.If(err.Ne(i2cOK), func(b *Block) { b.Return(err) })
		// The bytes: control bit 3 acknowledges each but the last.
		at := b.Tmp("at", m.Add(mailboxRead))
		b.Loop(func(b *Block) {
			control := b.Tmp("control", 0x58)
			b.If(reads.Eq(1), func(b *Block) { b.Set(control, 0x50) })
			b.Set(i2c(i2cControl), control)
			b.Set(err, wait.Call())
			b.If(err.Ne(i2cOK), func(b *Block) { b.Return(err) })
			b.Store8(at, i2c(i2cData))
			b.Inc(at, 1)
			b.Inc(reads, -1)
			b.If(reads.Eq(0), func(b *Block) { b.Break() })
		})
		b.Set(i2c(i2cControl), 0x40)
		b.Do(idle.Call())
		b.Return(i2cOK)
	})
	transfer := p.Func("transfer", 1)
	transfer.Define(func(b *Block) {
		err := b.Tmp("err", run.Call(transfer.Param(0)))
		b.If(err.Ne(i2cOK), func(b *Block) { b.Set(i2c(i2cControl), 0x40) })
		b.Return(err)
	})

	// maskOfRing waits for the ring's slot after slot, which carries a
	// modify's mask: the slot's word, or 0xFFFFFFFF when it never came.
	maskOfRing := p.Func("maskOfRing", 2) // slot, lap expected in it
	maskOfRing.Define(func(b *Block) {
		next, lap := maskOfRing.Param(0), maskOfRing.Param(1)
		b.Set(next, next.Add(1).And(ringSlots-1))
		b.If(next.Eq(0), func(b *Block) { b.Set(lap, lap.Add(ringLap)) })
		at := b.Tmp("at", K(ring).Add(next.Shl(2)))
		n := b.Tmp("n", i2cPolls)
		b.Loop(func(b *Block) {
			w := b.Tmp("w", Mem32(at))
			b.If(w.And(ringLap).Eq(lap), func(b *Block) { b.Return(w) })
			b.Inc(n, -1)
			b.If(n.Eq(0), func(b *Block) { b.Return(uint32(0xffffffff)) })
		})
	})

	// access runs a ring access (a slot's word; for a modify, mask is its
	// mask, or 0xFFFFFFFF when it never came) through the scratch mailbox:
	// it gives 0 or the error, a value read left at the scratch mailbox's
	// mailboxRead.
	access := p.Func("access", 2) // access, mask
	access.Define(func(b *Block) {
		op, mask := access.Param(0), access.Param(1)
		m := K(ringScratch)
		number := b.Tmp("number", op.Shr(16))
		next := b.Tmp("next", mailboxWrite+1) // where the next byte to write goes
		b.If(op.And(ringShort).Eq(0), func(b *Block) {
			b.Store8(m.Add(1), Mem8(K(ringAddresses)))
			b.Set(number, number.And(ringRegister))
			b.Store8(m.Add(mailboxWrite), number.Shr(8))
			b.Store8(m.Add(mailboxWrite+1), number)
			b.Set(next, mailboxWrite+2)
		}).Else(func(b *Block) {
			b.Store8(m.Add(1), Mem8(K(ringAddresses+1)))
			b.Store8(m.Add(mailboxWrite), number)
		})
		b.If(op.And(ringModify), func(b *Block) {
			// A modify: the register read, its bits outside the mask
			// cleared, the value or'ed in, written back.
			b.Store16(m.Add(modifyValue), op)
			b.If(mask.Eq(uint32(0xffffffff)), func(b *Block) { b.Return(i2cStuck) })
			b.Store16(m.Add(modifyMask), mask)
			b.Store8(m.Add(2), 2)
			b.Store8(m.Add(3), 2)
			err := b.Tmp("err", transfer.Call(m))
			b.If(err.Ne(i2cOK), func(b *Block) { b.Return(err) })
			v := b.Tmp("v", Mem16(m.Add(mailboxRead)).And(Mem16(m.Add(modifyMask))).Or(Mem16(m.Add(modifyValue))))
			b.Store8(m.Add(mailboxWrite+2), v.Shr(8))
			b.Store8(m.Add(mailboxWrite+3), v)
			b.Store8(m.Add(2), 4)
			b.Store8(m.Add(3), 0)
			b.Return(transfer.Call(m))
		})
		b.If(op.And(ringRead), func(b *Block) {
			b.Store8(m.Add(2), next.Sub(mailboxWrite))
			b.Store8(m.Add(3), 2)
		}).Else(func(b *Block) {
			b.Store8(m.Add(next), op.Shr(8))
			b.Store8(m.Add(next).Add(1), op)
			b.Store8(m.Add(2), next.Sub(mailboxWrite-2))
			b.Store8(m.Add(3), 0)
		})
		b.Return(transfer.Call(m))
	})

	// answerWord adds a word to the command packet's answer, unless it is
	// full.
	answerWord := p.Func("answerWord", 1)
	answerWord.Define(func(b *Block) {
		count := b.Tmp("count", vars(commandCount))
		b.If(count.Eq(commandReads), func(b *Block) { b.Return() })
		b.Store32(K(commandAnswers).Add(count.Shl(2)), answerWord.Param(0))
		b.Set(vars(commandCount), count.Add(1))
	})

	// memory runs a memory access of a command packet: its word op, the
	// words after it at vars' pointer (its address, then what it writes).
	memory := p.Func("memory", 1)
	memory.Define(func(b *Block) {
		op := memory.Param(0)
		ptr := b.Tmp("ptr", vars(commandPointer))
		left := b.Tmp("left", vars(commandLeft))
		at := b.Tmp("at", Mem32(ptr))
		b.Inc(ptr, 4)
		b.Inc(left, -1)
		n := b.Tmp("n", op.And(0xff))
		b.If(op.And(ringRead).Eq(0), func(b *Block) {
			// A write: one word, or bytes four to a word.
			b.If(op.And(memoryFIFO).Eq(0), func(b *Block) {
				b.Store32(at, Mem32(ptr))
				b.Inc(ptr, 4)
				b.Inc(left, -1)
			}).Else(func(b *Block) {
				b.While(n.Ne(0), func(b *Block) {
					w := b.Tmp("w", Mem32(ptr))
					b.Inc(ptr, 4)
					b.Inc(left, -1)
					k := b.Tmp("k", 4)
					b.While(n.Ne(0).AndAlso(k.Ne(0)), func(b *Block) {
						b.Store32(at, w.Shr(24))
						b.Set(w, w.Shl(8))
						b.Inc(n, -1)
						b.Inc(k, -1)
					})
				})
			})
		}).Else(func(b *Block) {
			// A read: one word, or bytes packed four to an answer word.
			b.If(op.And(memoryFIFO).Eq(0), func(b *Block) {
				b.Do(answerWord.Call(Mem32(at)))
			}).Else(func(b *Block) {
				w := b.Tmp("w", 0)
				c := b.Tmp("c", 0)
				b.While(n.Ne(0), func(b *Block) {
					b.Set(w, w.Shl(8).Or(Mem32(at).And(0xff)))
					b.Inc(c, 1)
					b.Inc(n, -1)
					b.If(c.Eq(4), func(b *Block) {
						b.Do(answerWord.Call(w))
						b.Set(w, 0)
						b.Set(c, 0)
					})
				})
				b.If(c.Ne(0), func(b *Block) {
					b.While(c.Ne(4), func(b *Block) {
						b.Set(w, w.Shl(8))
						b.Inc(c, 1)
					})
					b.Do(answerWord.Call(w))
				})
			})
		})
		b.Set(vars(commandPointer), ptr)
		b.Set(vars(commandLeft), left)
	})

	// packet runs a command packet at slot, whose sequence number it is:
	// its accesses, then, when asked for, the answer on endpoint 0x83: the
	// sequence number, the first error since the last answer with the
	// number of values, and the values.
	packet := p.Func("packet", 1)
	packet.Define(func(b *Block) {
		slot := packet.Param(0)
		flags := b.Tmp("flags", Mem32(slot.Add(4)))
		b.Set(vars(commandFlags), flags)
		b.Set(vars(commandLeft), flags.And(0xff))
		b.Set(vars(commandPointer), slot.Add(8))
		b.Set(vars(commandCount), 0)
		b.While(vars(commandLeft).Ne(0), func(b *Block) {
			ptr := b.Tmp("ptr", vars(commandPointer))
			left := b.Tmp("left", vars(commandLeft))
			op := b.Tmp("op", Mem32(ptr))
			b.Inc(ptr, 4)
			b.Inc(left, -1)
			kind := b.Tmp("kind", op.And(ringModify|ringMask))
			b.If(kind.Eq(ringMask), func(b *Block) {
				b.Set(vars(commandPointer), ptr)
				b.Set(vars(commandLeft), left)
				b.Do(memory.Call(op))
				b.Continue()
			})
			// A modify brings its mask in the next word.
			b.If(kind.Eq(ringModify), func(b *Block) {
				b.Set(vars(commandMask), Mem32(ptr))
				b.Inc(ptr, 4)
				b.Inc(left, -1)
			})
			b.Set(vars(commandPointer), ptr)
			b.Set(vars(commandLeft), left)
			b.Set(vars(commandOp), op)
			err := b.Tmp("err", access.Call(op, vars(commandMask)))
			// The error, if it is the first; the value, if a read's.
			b.If(err.Ne(i2cOK).AndAlso(vars(commandError).Eq(0)), func(b *Block) { b.Set(vars(commandError), err) })
			b.If(vars(commandOp).And(ringRead), func(b *Block) {
				b.Do(answerWord.Call(Mem16(K(ringScratch + mailboxRead))))
			})
		})
		b.If(vars(commandFlags).And(commandAnswer), func(b *Block) {
			fifo := K(answerFIFO)
			b.Store32(fifo, vars(commandExpect))
			count := b.Tmp("count", vars(commandCount))
			b.Store32(fifo, vars(commandError).Shl(16).Or(count))
			at := b.Tmp("at", commandAnswers)
			b.While(count.Ne(0), func(b *Block) {
				b.Store32(fifo, Mem32(at))
				b.Inc(at, 4)
				b.Inc(count, -1)
			})
			b.Store32(K(answerSend), 4)
			b.Set(vars(commandError), 0)
		})
		// The next slot.
		b.Set(vars(commandExpect), vars(commandExpect).Add(1))
		next := b.Tmp("next", slot.Add(commandSize))
		b.If(next.Eq(commandBase+commandSlots*commandSize), func(b *Block) { b.Set(next, commandBase) })
		b.Set(vars(commandSlot), next)
	})

	// The jobs (job.go).
	job := func(offset uint32) *Node { return word(jobArea + offset) }

	// delay runs the wait loop n times, counting the passes for the host. A new
	// job given meanwhile ends it, which stops the rate measurement.
	delay := p.Func("delay", 1)
	delay.Define(func(b *Block) {
		n := delay.Param(0)
		given := b.Tmp("given", job(jobSequence))
		b.While(n.Ne(0).AndAlso(job(jobSequence).Eq(given)), func(b *Block) {
			b.Set(job(jobSpin), job(jobSpin).Add(1))
			b.Inc(n, -1)
		})
	})

	// shortWrite and shortRead reach a register with a one-byte number through
	// the job's mailbox; shortRead gives the value, or the error in bits 16 up.
	shortWrite := p.Func("shortWrite", 3) // address, register, value
	shortWrite.Define(func(b *Block) {
		m := K(jobMailbox)
		value := shortWrite.Param(2)
		b.Store8(m.Add(1), shortWrite.Param(0))
		b.Store8(m.Add(2), 3)
		b.Store8(m.Add(3), 0)
		b.Store8(m.Add(mailboxWrite), shortWrite.Param(1))
		b.Store8(m.Add(mailboxWrite+1), value.Shr(8))
		b.Store8(m.Add(mailboxWrite+2), value)
		b.Return(transfer.Call(m))
	})
	shortRead := p.Func("shortRead", 2) // address, register
	shortRead.Define(func(b *Block) {
		m := K(jobMailbox)
		b.Store8(m.Add(1), shortRead.Param(0))
		b.Store8(m.Add(2), 1)
		b.Store8(m.Add(3), 2)
		b.Store8(m.Add(mailboxWrite), shortRead.Param(1))
		err := b.Tmp("err", transfer.Call(m))
		b.If(err.Ne(i2cOK), func(b *Block) { b.Return(err.Shl(16)) })
		b.Return(Mem16(m.Add(mailboxRead)))
	})

	// tableFind gives the address of key's entry, or 0; a key of 0 finds a free
	// one.
	tableFind := p.Func("tableFind", 1)
	tableFind.Define(func(b *Block) {
		key := tableFind.Param(0)
		at := b.Tmp("at", tableBase)
		b.While(at.LtU(tableHeader), func(b *Block) {
			b.If(Mem32(at).Eq(key), func(b *Block) { b.Return(at) })
			b.Inc(at, tableEntry)
		})
		b.Return(0)
	})
	// tableEntryFor gives key's entry, making one if there is none: a free
	// entry, or the next in turn, emptied.
	tableEntryFor := p.Func("tableEntryFor", 1)
	tableEntryFor.Define(func(b *Block) {
		key := tableEntryFor.Param(0)
		at := b.Tmp("at", tableFind.Call(key))
		b.If(at.Ne(0), func(b *Block) { b.Return(at) })
		b.Set(at, tableFind.Call(0))
		b.If(at.Eq(0), func(b *Block) {
			next := b.Tmp("next", word(tableNext))
			b.Set(at, K(tableBase).Add(next.Mul(tableEntry)))
			b.Set(word(tableNext), next.Add(1).And(tableEntries-1))
		})
		b.Store32(at.Add(4), 0)
		b.Store32(at.Add(8), 0)
		b.Store32(at.Add(12), 0)
		b.Store32(at, key)
		b.Return(at)
	})
	// int8 is the low byte of x, sign-extended.
	int8 := func(x *Node) *Node { return x.And(0xff).Xor(0x80).Sub(0x80) }

	// captrim is the job jobCaptrimKind (Bridge.Captrim). Out: the trim, its
	// distance from target, and whether it started from a remembered one.
	captrim := p.Func("captrim", 0)
	captrim.Define(func(b *Block) {
		address := b.Tmp("address", job(jobParam))
		lo4 := b.Tmp("lo4", job(jobParam+4))
		c := b.Tmp("c", job(jobParam+8))
		step := b.Tmp("step", job(jobParam+12))
		target := b.Tmp("target", job(jobParam+16))
		key := b.Tmp("key", job(jobParam+32))
		b.Set(job(jobResult+8), 0)
		b.If(key.Ne(0), func(b *Block) {
			at := b.Tmp("at", tableFind.Call(key))
			b.If(at.Ne(0).AndAlso(Mem32(at.Add(4)).And(tableCaptrim).Ne(0)), func(b *Block) {
				b.Set(c, int8(Mem32(at.Add(4))))
				b.Set(step, rememberedStep)
				b.Set(job(jobResult+8), 1)
			})
		})
		best := b.Tmp("best", c)
		diff := b.Tmp("diff", 3000)
		b.Do(delay.Call(job(jobParam + 20)))
		b.Loop(func(b *Block) {
			b.Set(step, step.Sar(1))
			err := b.Tmp("err", shortWrite.Call(address, 0x18, lo4.Or(c.And(0xffff))))
			b.If(err.Ne(i2cOK), func(b *Block) { b.Return(err) })
			b.Do(delay.Call(job(jobParam + 24)))
			r := b.Tmp("r", shortRead.Call(address, 0x1d))
			b.If(r.Shr(16).Ne(0), func(b *Block) { b.Return(r.Shr(16)) })
			adc := b.Tmp("adc", r)
			b.If(adc.Ge(target), func(b *Block) {
				b.Set(adc, adc.Sub(target))
				b.If(adc.Lt(diff), func(b *Block) {
					b.Set(diff, adc)
					b.Set(best, c)
				})
				b.Set(c, c.Sub(step))
			}).Else(func(b *Block) {
				b.Set(adc, target.Sub(adc))
				b.If(adc.Lt(diff), func(b *Block) {
					b.Set(diff, adc)
					b.Set(best, c)
				})
				b.Set(c, c.Add(step))
			})
			b.Set(c, int8(c))
			b.Do(delay.Call(job(jobParam + 28)))
			b.If(step.Lt(1), func(b *Block) { b.Break() })
		})
		b.Set(job(jobResult), best)
		b.Set(job(jobResult+4), diff)
		b.If(key.Ne(0), func(b *Block) {
			at := b.Tmp("at", tableEntryFor.Call(key))
			b.Store32(at.Add(4), Mem32(at.Add(4)).And(tableTMCC).Or(tableCaptrim).Or(best.And(0xff)))
		})
		b.Return(shortWrite.Call(address, 0x18, lo4.Or(best.And(0xffff))))
	})

	// longRead reads a register with a two-byte number, as shortRead does.
	longRead := p.Func("longRead", 2) // address, register
	longRead.Define(func(b *Block) {
		m := K(jobMailbox)
		register := longRead.Param(1)
		b.Store8(m.Add(1), longRead.Param(0))
		b.Store8(m.Add(2), 2)
		b.Store8(m.Add(3), 2)
		b.Store8(m.Add(mailboxWrite), register.Shr(8))
		b.Store8(m.Add(mailboxWrite+1), register)
		err := b.Tmp("err", transfer.Call(m))
		b.If(err.Ne(i2cOK), func(b *Block) { b.Return(err.Shl(16)) })
		b.Return(Mem16(m.Add(mailboxRead)))
	})

	// tmccRecall is the job jobTMCCRecallKind: whether the key has parameters,
	// and them.
	tmccRecall := p.Func("tmccRecall", 0)
	tmccRecall.Define(func(b *Block) {
		b.Set(job(jobResult), 0)
		at := b.Tmp("at", tableFind.Call(job(jobParam)))
		b.If(at.Eq(0).OrElse(job(jobParam).Eq(0)), func(b *Block) { b.Return(i2cOK) })
		b.If(Mem32(at.Add(4)).And(tableTMCC).Eq(0), func(b *Block) { b.Return(i2cOK) })
		b.Set(job(jobResult+4), Mem32(at.Add(8)))
		b.Set(job(jobResult+8), Mem32(at.Add(12)))
		b.Set(job(jobResult), 1)
		b.Return(i2cOK)
	})

	// tmccRemember is the job jobTMCCRememberKind: it packs the TMCC registers as
	// TMCC says and keeps them. Out: whether they changed, and the two words.
	tmccRemember := p.Func("tmccRemember", 0)
	tmccRemember.Define(func(b *Block) {
		address := b.Tmp("address", job(jobParam))
		key := b.Tmp("key", job(jobParam+4))
		// read gives a register, or leaves the job with its error.
		read := func(b *Block, register uint32) *Node {
			r := b.Tmp("r", longRead.Call(address, register))
			b.If(r.Shr(16).Ne(0), func(b *Block) { b.Return(r.Shr(16)) })
			return r
		}
		first := b.Tmp("first", read(b, 570).And(0x73))
		b.Set(first, first.Or(read(b, 505).And(1).Shl(8)))
		layers := b.Tmp("layers", 0)
		for i := uint32(0); i < 3; i++ {
			layer := b.Tmp("layer", read(b, 493+i).And(0x0f))
			b.Set(layer, layer.Or(read(b, 499+i).And(3).Shl(4)))
			b.Set(layer, layer.Or(read(b, 481+i).And(7).Shl(6)))
			b.Set(layer, layer.Or(read(b, 487+i).And(3).Shl(9)))
			switch i {
			case 0:
				b.Set(first, first.Or(layer.Shl(12)))
			case 1:
				b.Set(layers, layer)
			case 2:
				b.Set(layers, layers.Or(layer.Shl(16)))
			}
		}
		b.Set(job(jobResult), 0)
		b.Set(job(jobResult+4), first)
		b.Set(job(jobResult+8), layers)
		b.If(key.Eq(0), func(b *Block) { b.Return(i2cOK) })
		at := b.Tmp("at", tableEntryFor.Call(key))
		was := b.Tmp("was", Mem32(at.Add(4)))
		b.If(was.And(tableTMCC).Ne(0).AndAlso(Mem32(at.Add(8)).Ne(first).OrElse(Mem32(at.Add(12)).Ne(layers))), func(b *Block) {
			b.Set(job(jobResult), 1)
		})
		b.Store32(at.Add(8), first)
		b.Store32(at.Add(12), layers)
		b.Store32(at.Add(4), was.Or(tableTMCC))
		b.Return(i2cOK)
	})

	main.Define(func(b *Block) {
		lap := b.Tmp("lap", ringLap)     // the lap expected in the next slot
		slot := b.Tmp("slot", ringSlots) // the number of the next slot
		served := b.Tmp("served", 0)     // the last mailbox sequence served
		b.Loop(func(b *Block) {
			b.Set(word(mailbox+mailboxAlive), word(mailbox+mailboxAlive).Add(1))

			// A command packet, if the slot holds the one expected: its
			// sequence number both first and last, the last written last.
			expect := b.Tmp("expect", vars(commandExpect))
			at := b.Tmp("at", vars(commandSlot))
			b.If(Mem32(at.Add(commandSize-4)).Eq(expect).AndAlso(Mem32(at).Eq(expect)), func(b *Block) {
				b.Do(packet.Call(at))
				b.Continue()
			})

			// The next ring slot, if its lap is the one expected.
			w := b.Tmp("w", Mem32(K(ring).Add(slot.And(ringSlots-1).Shl(2))))
			b.If(w.And(ringLap).Eq(lap), func(b *Block) {
				err := b.Tmp("err", i2cOK)
				// The slot after a modify carries only its mask.
				b.If(w.And(ringMask).Eq(0), func(b *Block) {
					mask := b.Tmp("mask", 0)
					b.If(w.And(ringModify), func(b *Block) { b.Set(mask, maskOfRing.Call(slot, lap)) })
					b.Set(err, access.Call(w, mask))
				})
				// The answer: slot number, error, value read.
				answer := b.Tmp("answer", slot.Shl(24).Or(err.Shl(16)))
				b.Store32(K(ringAnswer), answer.Or(Mem16(K(ringScratch+mailboxRead))))
				b.If(err.Ne(i2cOK).AndAlso(word(ringFailure).Eq(0)), func(b *Block) { b.Store32(K(ringFailure), answer) })
				b.Inc(slot, 1)
				b.If(slot.And(ringSlots-1).Eq(0), func(b *Block) { b.Set(lap, lap.Add(ringLap)) })
				b.Continue()
			})

			// A job, if the host has given one since the last done.
			b.If(job(jobSequence).Ne(job(jobDone)), func(b *Block) {
				kind := b.Tmp("kind", job(jobKind))
				err := b.Tmp("err", i2cOK)
				b.If(kind.Eq(jobSpinKind), func(b *Block) { b.Do(delay.Call(job(jobParam))) })
				b.If(kind.Eq(jobCaptrimKind), func(b *Block) { b.Set(err, captrim.Call()) })
				b.If(kind.Eq(jobTMCCRecallKind), func(b *Block) { b.Set(err, tmccRecall.Call()) })
				b.If(kind.Eq(jobTMCCRememberKind), func(b *Block) { b.Set(err, tmccRemember.Call()) })
				b.Set(job(jobError), err)
				b.Set(job(jobDone), job(jobSequence))
				b.Continue()
			})

			// The mailbox.
			seq := b.Tmp("seq", Mem8(K(mailbox)))
			b.If(seq.Eq(served), func(b *Block) { b.Continue() })
			b.Set(served, seq)
			b.Store32(K(mailbox+mailboxAnswer), transfer.Call(K(mailbox)))
			b.Store8(K(mailbox+mailboxAnswer), seq)
		})
	})
	return p.Words()
}
