# Gera rsrc_windows_amd64.syso (COFF .rsrc) com ícone + manifesto, sem ferramentas externas.
import struct, pickle, sys

icons = pickle.load(open(sys.argv[1], 'rb'))  # [(size, bytes)]
manifest = open(sys.argv[2], 'rb').read()
out = sys.argv[3]

RT_ICON, RT_GROUP_ICON, RT_MANIFEST = 3, 14, 24
LANG = 0x0409

grp = struct.pack('<HHH', 0, 1, len(icons))
for i, (s, data) in enumerate(icons):
    w = 0 if s >= 256 else s
    grp += struct.pack('<BBBBHHIH', w, w, 0, 0, 1, 32, len(data), i + 1)

# (tipo, [(id, dados)])
types = [
    (RT_ICON, [(i + 1, d) for i, (_, d) in enumerate(icons)]),
    (RT_GROUP_ICON, [(1, grp)]),
    (RT_MANIFEST, [(1, manifest)]),
]

def dirhdr(n):
    return struct.pack('<IIHHHH', 0, 0, 0, 0, 0, n)

# calcula offsets: raiz, dirs de tipo, dirs de nome, dirs de idioma, entradas de dados, blobs
root_size = 16 + 8 * len(types)
off = root_size
type_dir_off = []
for t, items in types:
    type_dir_off.append(off)
    off += 16 + 8 * len(items)
name_dir_off = []
for t, items in types:
    lst = []
    for _ in items:
        lst.append(off)
        off += 16 + 8
    name_dir_off.append(lst)
data_entry_off = []
for t, items in types:
    lst = []
    for _ in items:
        lst.append(off)
        off += 16
    data_entry_off.append(lst)
blob_off = []
for t, items in types:
    lst = []
    for _, d in items:
        off = (off + 7) & ~7
        lst.append(off)
        off += len(d)
    blob_off.append(lst)
total = (off + 7) & ~7

buf = bytearray(total)
relocs = []
buf[0:16] = dirhdr(len(types))
for i, (t, items) in enumerate(types):
    struct.pack_into('<II', buf, 16 + 8 * i, t, 0x80000000 | type_dir_off[i])
    o = type_dir_off[i]
    buf[o:o + 16] = dirhdr(len(items))
    for j, (rid, d) in enumerate(items):
        struct.pack_into('<II', buf, o + 16 + 8 * j, rid, 0x80000000 | name_dir_off[i][j])
        n = name_dir_off[i][j]
        buf[n:n + 16] = dirhdr(1)
        struct.pack_into('<II', buf, n + 16, LANG, data_entry_off[i][j])
        e = data_entry_off[i][j]
        struct.pack_into('<IIII', buf, e, blob_off[i][j], len(d), 0, 0)
        relocs.append(e)  # o campo RVA precisa de relocação
        b = blob_off[i][j]
        buf[b:b + len(d)] = d

# COFF
hdr_size = 20 + 40
raw_ptr = hdr_size
reloc_ptr = raw_ptr + total
sym_ptr = reloc_ptr + 10 * len(relocs)
coff = struct.pack('<HHIIIHH', 0x8664, 1, 0, sym_ptr, 1, 0, 0)
sect = struct.pack('<8sIIIIIIHHI', b'.rsrc', 0, 0, total, raw_ptr, reloc_ptr, 0, len(relocs), 0, 0x40000040)
rel = b''.join(struct.pack('<IIH', r, 0, 3) for r in relocs)  # IMAGE_REL_AMD64_ADDR32NB
sym = struct.pack('<8sIhHBB', b'.rsrc', 0, 1, 0, 3, 0)
strtab = struct.pack('<I', 4)
open(out, 'wb').write(coff + sect + bytes(buf) + rel + sym + strtab)
print('ok', total, 'bytes de recurso,', len(relocs), 'relocações')
