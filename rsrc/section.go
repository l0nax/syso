package rsrc

import (
	"encoding/binary"
	"errors"
	"io"
	"unicode/utf16"

	"github.com/hallazzang/syso/coff"
	"github.com/hallazzang/syso/internal/common"
)

// Section is a .rsrc section.
type Section struct {
	rootDir     *resourceDirectory
	relocations []coff.Relocation
}

// New returns newly created .rsrc section.
func New() *Section {
	return &Section{
		rootDir: &resourceDirectory{},
	}
}

// Name returns section's name, which is .rsrc in general.
func (s *Section) Name() string {
	return ".rsrc"
}

// Size returns section's size. It traverses the section's tree
// structure internally when it called.
func (s *Section) Size() int {
	return int(s.freeze())
}

func (s *Section) Relocations() []coff.Relocation {
	s.freeze()
	return s.relocations
}

func (s *Section) freeze() uint32 {
	var offset uint32

	s.relocations = nil

	s.rootDir.walk(func(dir *resourceDirectory) error {
		// TODO: instead of calculating size of newly created dummy structure,
		// use fixed constant.
		dir.offset = offset
		offset += uint32(binary.Size(&rawResourceDirectory{}))
		for _, e := range dir.entries() {
			e.offset = offset
			offset += uint32(binary.Size(&rawResourceDirectoryEntry{}))
		}
		return nil
	})

	s.rootDir.walk(func(dir *resourceDirectory) error {
		for _, str := range dir.strings {
			str.offset = offset
			// TODO: should we encode string to calculate its utf-16
			// encoded size? better solution may exist.
			offset += 2 + uint32(len(utf16.Encode([]rune(str.string))))
		}
		return nil
	})

	s.rootDir.walk(func(dir *resourceDirectory) error {
		for _, e := range dir.dataEntries() {
			e.offset = offset
			offset += uint32(binary.Size(&resourceDataEntry{}))
		}
		return nil
	})

	s.rootDir.walk(func(dir *resourceDirectory) error {
		for _, d := range dir.datas() {
			s.relocations = append(s.relocations, coff.Relocation{
				VirtualAddress: offset,
			})
			d.offset = offset
			offset += uint32(d.Size())
		}
		return nil
	})

	return offset
}

// WriteTo writes section data to w.
func (s *Section) WriteTo(w io.Writer) (int64, error) {
	var written int64

	s.freeze()

	if err := s.rootDir.walk(func(dir *resourceDirectory) error {
		n, err := common.BinaryWriteTo(w, &rawResourceDirectory{
			Characteristics:     dir.characteristics,
			NumberOfNameEntries: uint16(len(dir.nameEntries)),
			NumberOfIDEntries:   uint16(len(dir.idEntries)),
		})
		if err != nil {
			return err
		}
		written += n

		for _, e := range dir.entries() {
			var i uint32
			if e.name != nil {
				i = e.name.offset
			} else {
				i = uint32(*e.id)
			}
			var o uint32
			if e.dataEntry != nil {
				o = e.dataEntry.offset
			} else {
				o = e.subdirectory.offset
			}
			n, err := common.BinaryWriteTo(w, &rawResourceDirectoryEntry{
				NameOffsetOrIntegerID:               i,
				DataEntryOffsetOrSubdirectoryOffset: o,
			})
			if err != nil {
				return err
			}
			written += n
		}

		return nil
	}); err != nil {
		return written, err
	}

	if err := s.rootDir.walk(func(dir *resourceDirectory) error {
		for _, str := range dir.strings {
			u := utf16.Encode([]rune(str.string))
			n, err := common.BinaryWriteTo(w, uint16(len(u)))
			if err != nil {
				return err
			}
			written += n
			n, err = common.BinaryWriteTo(w, u)
			if err != nil {
				return err
			}
			written += n
		}
		return nil
	}); err != nil {
		return written, err
	}

	if err := s.rootDir.walk(func(dir *resourceDirectory) error {
		for _, e := range dir.dataEntries() {
			n, err := common.BinaryWriteTo(w, &rawResourceDataEntry{
				DataRVA: e.data.offset,
				Size:    uint32(e.data.Size()),
			})
			if err != nil {
				return err
			}
			written += n
		}
		return nil
	}); err != nil {
		return written, err
	}

	if err := s.rootDir.walk(func(dir *resourceDirectory) error {
		for _, d := range dir.datas() {
			n, err := io.CopyN(w, d, d.Size())
			if err != nil {
				return err
			}
			written += n
		}
		return nil
	}); err != nil {
		return written, err
	}

	return written, nil
}

func (s *Section) addResource(typ int, id *int, name *string, blob Blob) error {
	var subdir *resourceDirectory
	for _, e := range s.rootDir.idEntries {
		if *e.id == typ {
			if e.subdirectory == nil {
				return errors.New("subdirectory should exist")
			}
			subdir = e.subdirectory
		}
	}
	if subdir == nil {
		subdir = s.rootDir.addSubdirectoryByID(typ, 0)
	}

	if id != nil {
		for _, e := range subdir.idEntries {
			if *e.id == *id {
				return errors.New("duplicate resource id")
			}
		}
		subdir = subdir.addSubdirectoryByID(*id, 0)
	} else {
		for _, e := range subdir.nameEntries {
			if e.name.string == *name {
				return errors.New("duplicate resource name")
			}
		}
		subdir = subdir.addSubdirectoryByName(*name, 0)
	}

	subdir.addDataEntryByID(enUSLanguage, blob)

	return nil
}

// AddIconByID adds an icon resource identified by an integer id
// to section.
func (s *Section) AddIconByID(id int, blob Blob) error {
	return s.addResource(iconResource, &id, nil, blob)
}

// AddIconByName adds an icon resource identified by name to section.
func (s *Section) AddIconByName(name string, blob Blob) error {
	return s.addResource(iconResource, nil, &name, blob)
}
