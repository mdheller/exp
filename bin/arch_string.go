// Code generated by "stringer -linecomment -type Arch"; DO NOT EDIT.

package bin

import "strconv"

const _Arch_name = "x86_32x86_64MIPS_32PowerPC_32"

var _Arch_index = [...]uint8{0, 6, 12, 19, 29}

func (i Arch) String() string {
	i -= 1
	if i >= Arch(len(_Arch_index)-1) {
		return "Arch(" + strconv.FormatInt(int64(i+1), 10) + ")"
	}
	return _Arch_name[_Arch_index[i]:_Arch_index[i+1]]
}
