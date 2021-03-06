package main

import (
	"bytes"
	"fmt"
	"io/ioutil"
	"path/filepath"
	"strings"

	"github.com/decomp/exp/bin"
	"github.com/decomp/exp/disasm/x86"
	"github.com/mewrev/pe"
	"github.com/pkg/errors"
	"golang.org/x/arch/x86/x86asm"
)

// dumpSections dumps the given sections in NASM syntax.
func dumpSections(sects []*bin.Section, file *pe.File, fs []*x86.Func) error {
	// Index functions, basic blocks and instructions.
	funcs := make(map[bin.Address]*x86.Func)
	blocks := make(map[bin.Address]*x86.BasicBlock)
	insts := make(map[bin.Address]*x86.Inst)
	for _, f := range fs {
		funcs[f.Addr] = f
		for _, block := range f.Blocks {
			blocks[block.Addr] = block
			for _, inst := range block.Insts {
				insts[inst.Addr] = inst
			}
			if !block.Term.IsDummyTerm() {
				insts[block.Term.Addr] = block.Term
			}
		}
	}
	optHdr, err := file.OptHeader()
	if err != nil {
		return errors.WithStack(err)
	}
	entry := bin.Address(optHdr.ImageBase + optHdr.EntryRelAddr)
	imageBase := bin.Address(optHdr.ImageBase)
	dataDirs := optHdr.DataDirs
	for _, sect := range sects {
		if len(sect.Name) == 0 {
			// Ignore segments.
			continue
		}
		data := func(addr bin.Address) (byte, bool) {
			i := int(addr - sect.Addr)
			if i < 0 || i >= len(sect.Data) {
				return 0, false
			}
			return sect.Data[addr-sect.Addr], true
		}
		buf := dumpSection(sect, entry, imageBase, dataDirs, funcs, blocks, insts, data)
		filename := strings.Replace(sect.Name, ".", "_", -1) + ".asm"
		outPath := filepath.Join(outDir, filename)
		dbg.Printf("creating %q\n", outPath)
		if err := ioutil.WriteFile(outPath, buf, 0644); err != nil {
			return errors.WithStack(err)
		}
	}
	return nil
}

// dumpSection dumps the given section in NASM syntax.
func dumpSection(sect *bin.Section, entry, imageBase bin.Address, dataDirs []pe.DataDirectory, funcs map[bin.Address]*x86.Func, blocks map[bin.Address]*x86.BasicBlock, insts map[bin.Address]*x86.Inst, data func(addr bin.Address) (byte, bool)) []byte {
	buf := &bytes.Buffer{}
	sectName := strings.Replace(sect.Name, ".", "_", -1)
	// Dump section header.
	//
	//    ; <.text>
	//    ;
	//    ;    file offset:    0x00000400
	//    ;    virtual offset: 0x00401000
	//
	//    SECTION .text
	const sectHeader = `
; <%s>
;
;    file offset:    0x%08X
;    virtual offset: 0x%08X

SECTION %s

`
	fmt.Fprintf(buf, sectHeader[1:], sect.Name, sect.Offset, uint64(sect.Addr), sect.Name)
	end := sect.Addr + bin.Address(len(sect.Data))
	// Import table.
	itAddr := imageBase + bin.Address(dataDirs[1].RelAddr)
	itEnd := itAddr + bin.Address(dataDirs[1].Size)
	// Resource table.
	rsrcTableAddr := imageBase + bin.Address(dataDirs[2].RelAddr)
	rsrcTableEnd := rsrcTableAddr + bin.Address(dataDirs[2].Size)
	// Import address table.
	iatAddr := imageBase + bin.Address(dataDirs[12].RelAddr)
	iatEnd := iatAddr + bin.Address(dataDirs[12].Size)
	for addr := sect.Addr; addr <= end; {
		switch addr {
		case entry:
			buf.WriteString("\nstart:\n")
		case itAddr:
			buf.WriteString("\nimport_table:\n")
		case itEnd:
			buf.WriteString("\n   import_table_size    equ     $ - import_table\n")
		case rsrcTableAddr:
			buf.WriteString("\nresource_table:\n")
		case rsrcTableEnd:
			buf.WriteString("\n   resource_table_size  equ     $ - $$\n")
		case iatAddr:
			buf.WriteString("\niat:\n")
		case iatEnd:
			buf.WriteString("\niat_size             equ     $ - iat\n")
		}
		a := uint64(addr)
		if sect.Perm&bin.PermX != 0 {
			// Dump function header.
			//
			//    times (0x401000 - _text_vstart) - ($ - $$) db 0xCC
			//    sub_401000:
			if _, ok := funcs[addr]; ok {
				if addr != sect.Addr {
					buf.WriteString("\n")
				}
				const funcHeader = `
times (0x%06X - %s_vstart) - ($ - $$) db 0xCC
sub_%06X:
`
				fmt.Fprintf(buf, funcHeader[1:], a, sectName, a)
			}
			// Dump basic block header.
			if _, ok := blocks[addr]; ok {
				fmt.Fprintf(buf, "; block_%06X\n", a)
			}
			// Dump instruction.
			//
			//    addr_401000:          db      0x83, 0xEC, 0x08                                ; sub    esp,0x8
			if inst, ok := insts[addr]; ok {
				fmt.Fprintf(buf, "  addr_%06X:          db      ", a)
				for i := 0; i < inst.Len; i++ {
					if i != 0 {
						fmt.Fprint(buf, ", ")
					}
					b, ok := data(addr + bin.Address(i))
					if !ok {
						panic(fmt.Errorf("unable to locate data at %v", addr+bin.Address(i)))
					}
					fmt.Fprintf(buf, "0x%02X", b)

				}
				pad := " "
				if n := 80 - (len("  addr_401000:          db      ") + len("0x00")*inst.Len + len(", ")*(inst.Len-1)); n > 0 {
					pad = strings.Repeat(" ", n)
				}
				fmt.Fprintf(buf, "%s; %s\n", pad, x86asm.IntelSyntax(inst.Inst, uint64(addr), nil))
				addr += bin.Address(inst.Len)
				continue
			}
		}

		// Dump data.
		//
		//    addr_48B054:          db      0x44 ; 'D'
		if b, ok := data(addr); ok {
			char := ""
			if isPrint(b) {
				char = fmt.Sprintf(" ; %q", b)
			}
			fmt.Fprintf(buf, "  addr_%06X:          db      0x%02X%s\n", a, b, char)
		}
		addr++
	}

	// The virtual size (sect.MemSize) is larger in unitialized sections, and the
	// raw size (len(sect.Data)) is larger in sections with padding.
	if sect.MemSize > len(sect.Data) {
		// Section with uninitialized data.
		//
		//       _data_size           equ     $ - $$
		//
		//    ; Uninitialized data (allocated by the linker).
		//    ;times _data_vsize - ($ - $$) resb 1
		const sectFooter = `
   %s_size%sequ     $ - $$

; Uninitialized data (allocated by the linker).
;times %s_vsize - ($ - $$) resb 1
`
		pad := " "
		if n := 24 - (len("   ") + len(sectName) + len("_size")); n > 0 {
			pad = strings.Repeat(" ", n)
		}
		fmt.Fprintf(buf, sectFooter, sectName, pad, sectName)
	} else {
		// Section with padding.
		//
		//       _text_vsize          equ     $ - $$
		//
		//    ; Section alignment.
		//    times _text_size - ($ - $$) db 0x00
		const sectFooter = `
   %s_vsize%sequ     $ - $$

; Section alignment.
times %s_size - ($ - $$) db 0x00
`
		pad := " "
		if n := 24 - (len("   ") + len(sectName) + len("_vsize")); n > 0 {
			pad = strings.Repeat(" ", n)
		}
		fmt.Fprintf(buf, sectFooter, sectName, pad, sectName)
	}
	return buf.Bytes()
}
