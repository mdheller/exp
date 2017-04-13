// The bin2ll tool converts binary executables to equivalent LLVM IR assembly
// (*.exe -> *.ll).
package main

import (
	"bufio"
	"debug/pe"
	"encoding/json"
	"flag"
	"fmt"
	"io/ioutil"
	"log"
	"os"
	"sort"

	"github.com/decomp/exp/bin"
	"github.com/kr/pretty"
	"github.com/mewkiz/pkg/term"
	"github.com/pkg/errors"
)

// dbg represents a logger with the "bin2ll:" prefix, which logs debug messages
// to standard error.
var dbg = log.New(os.Stderr, term.MagentaBold("bin2ll:")+" ", 0)

func usage() {
	const use = `
Convert binary executables to equivalent LLVM IR assembly (*.exe -> *.ll).

Usage:

	bin2ll [OPTION]... FILE.ll

Flags:
`
	fmt.Fprint(os.Stderr, use[1:])
	flag.PrintDefaults()
}

func main() {
	// Parse command line arguments.
	var (
		// funcAddr specifies a function address to decompile.
		funcAddr bin.Address
		// quiet specifies whether to suppress non-error messages.
		quiet bool
	)
	flag.Usage = usage
	flag.Var(&funcAddr, "func", "function address to decompile")
	flag.BoolVar(&quiet, "q", false, "suppress non-error messages")
	flag.Parse()
	if flag.NArg() != 1 {
		flag.Usage()
		os.Exit(1)
	}
	binPath := flag.Arg(0)
	// Mute debug messages if `-q` is set.
	if quiet {
		dbg.SetOutput(ioutil.Discard)
	}

	// Convert binary into LLVM IR assembly.
	d, err := parseFile(binPath)
	if err != nil {
		log.Fatalf("%+v", err)
	}
	defer d.file.Close()

	// Translate functions from x86 machine code to LLVM IR assembly.
	funcAddrs := d.funcAddrs
	if funcAddr != 0 {
		funcAddrs = []bin.Address{funcAddr}
	}
	for _, funcAddr := range funcAddrs {
		f, err := d.decodeFunc(funcAddr)
		if err != nil {
			log.Fatalf("%+v", err)
		}
		pretty.Println("f:", f)
	}
}

// A disassembler tracks information required to disassemble x86 executables.
type disassembler struct {
	// PE file.
	file *pe.File
	// Function addresses.
	funcAddrs []bin.Address
	// Basic block addresses.
	blockAddrs []bin.Address
	// Chunks of bytes.
	chunks []Chunk
}

// parseFile parses the given PE file and associated JSON files, containing
// information required to disassemble the x86 executables.
func parseFile(binPath string) (*disassembler, error) {
	// Parse PE executable.
	f, err := pe.Open(binPath)
	if err != nil {
		return nil, errors.WithStack(err)
	}
	d := &disassembler{
		file: f,
	}

	// Parse function addresses.
	funcAddrs, err := parseAddrs("funcs.json")
	if err != nil {
		return nil, errors.WithStack(err)
	}
	sort.Sort(bin.Addresses(funcAddrs))
	d.funcAddrs = funcAddrs

	// Parse basic block addresses.
	blockAddrs, err := parseAddrs("blocks.json")
	if err != nil {
		return nil, errors.WithStack(err)
	}
	sort.Sort(bin.Addresses(blockAddrs))
	d.blockAddrs = blockAddrs

	// Parse data addresses (e.g. jump tables).
	dataAddrs, err := parseAddrs("data.json")
	if err != nil {
		return nil, errors.WithStack(err)
	}
	sort.Sort(bin.Addresses(dataAddrs))

	// Append basic blocks as code chunks.
	for _, blockAddr := range blockAddrs {
		chunk := Chunk{
			kind: kindCode,
			addr: blockAddr,
		}
		d.chunks = append(d.chunks, chunk)
	}

	// Append data as data chunks.
	for _, dataAddr := range dataAddrs {
		chunk := Chunk{
			kind: kindData,
			addr: dataAddr,
		}
		d.chunks = append(d.chunks, chunk)
	}
	less := func(i, j int) bool {
		return d.chunks[i].addr < d.chunks[j].addr
	}
	sort.Slice(d.chunks, less)

	return d, nil
}

// Chunk represents a chunk of bytes.
type Chunk struct {
	// Chunk kind.
	kind kind
	// Chunk address.
	addr bin.Address
}

// kind represents the set of chunk kinds.
type kind uint

// Chunk kinds.
const (
	kindNone kind = iota
	kindCode
	kindData
)

// ### [ Helper functions ] ####################################################

// parseAddrs parses the given JSON file and returns the list of addresses
// contained within.
func parseAddrs(jsonPath string) ([]bin.Address, error) {
	var addrs []bin.Address
	if err := parseJSON(jsonPath, &addrs); err != nil {
		return nil, errors.WithStack(err)
	}
	return addrs, nil
}

// parseJSON parses the given JSON file and stores the result into v.
func parseJSON(jsonPath string, v interface{}) error {
	f, err := os.Open(jsonPath)
	if err != nil {
		return errors.WithStack(err)
	}
	defer f.Close()
	br := bufio.NewReader(f)
	dec := json.NewDecoder(br)
	return dec.Decode(v)
}
