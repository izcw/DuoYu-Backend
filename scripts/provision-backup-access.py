"""Grant the backend exact backup access, independent of supplementary groups.
Run as root when provisioning and before backup creation; never makes backups public.
"""
import ctypes
import ctypes.util
import os
import pathlib
import pwd
import re
import stat

if os.geteuid() != 0:
    raise SystemExit('Administrator privileges required')
uid = pwd.getpwnam('duoyu-backend').pw_uid
root = pathlib.Path('/www/backup')
directory = root / 'duoyu-sync'
for path in (root, directory):
    if not stat.S_ISDIR(path.lstat().st_mode) or path.is_symlink():
        raise SystemExit('Expected a real backup directory')
lib = ctypes.CDLL(ctypes.util.find_library('acl'), use_errno=True)
lib.acl_get_file.argtypes = [ctypes.c_char_p, ctypes.c_int]
lib.acl_get_file.restype = ctypes.c_void_p
lib.acl_to_text.argtypes = [ctypes.c_void_p, ctypes.c_void_p]
lib.acl_to_text.restype = ctypes.c_void_p
lib.acl_from_text.argtypes = [ctypes.c_char_p]
lib.acl_from_text.restype = ctypes.c_void_p
lib.acl_set_file.argtypes = [ctypes.c_char_p, ctypes.c_int, ctypes.c_void_p]
lib.acl_free.argtypes = [ctypes.c_void_p]
ACCESS, DEFAULT = 0x8000, 0x4000

def read_acl(path, kind):
    acl = lib.acl_get_file(os.fsencode(path), kind)
    if not acl:
        raise OSError(ctypes.get_errno(), 'Cannot read backup ACL')
    text = lib.acl_to_text(acl, None)
    if not text:
        error = ctypes.get_errno()
        lib.acl_free(acl)
        raise OSError(error, 'Cannot decode backup ACL')
    try:
        return ctypes.string_at(text).decode()
    finally:
        lib.acl_free(text)
        lib.acl_free(acl)

def write_acl(path, text, kind=ACCESS):
    acl = lib.acl_from_text(text.encode())
    if not acl:
        raise OSError(ctypes.get_errno(), 'Invalid backup ACL')
    try:
        if lib.acl_set_file(os.fsencode(path), kind, acl) != 0:
            raise OSError(ctypes.get_errno(), 'Cannot grant backup access')
    finally:
        lib.acl_free(acl)

def add_backend_access(text, permissions, owner_execute=False):
    entries = {}
    for line in text.splitlines():
        line = line.split('#', 1)[0].strip()
        if line:
            key, value = line.rsplit(':', 1)
            entries[key] = value
    # The mask applies to every named user and group, including the owning
    # group. Preserve their effective access when granting rights to the API.
    old_mask = entries.get('mask:', entries['group:'])
    added = set(permissions) - set(old_mask) - {'-'}
    for key, value in entries.items():
        if key.startswith('group:') or (key.startswith('user:') and key != 'user:'):
            entries[key] = ''.join('-' if bit in added else bit for bit in value)
    entries.pop('user:duoyu-backend', None)
    entries['user:' + str(uid)] = permissions
    entries['mask:'] = ''.join(c if c in old_mask + permissions else '-' for c in 'rwx')
    if owner_execute:
        entries['user:'] = entries['user:'][:2] + 'x'
    return '\n'.join(key + ':' + value for key, value in entries.items())


def grant(path, permissions, owner_execute=False):
    write_acl(path, add_backend_access(read_acl(path, ACCESS), permissions, owner_execute))


grant(root, '--x', owner_execute=True)
grant(directory, 'r-x')
# Mode 0600 during writing masks this grant; final chmod 0640 enables read only.
default = read_acl(directory, DEFAULT).strip()
if not default:
    default = 'user::rwx\ngroup::r-x\nmask::r-x\nother::---'
write_acl(directory, add_backend_access(default, 'r-x'), DEFAULT)
count = 0
for path in directory.iterdir():
    if re.fullmatch(r'sync-[\dTZ:.\-]+\.(dump|assets\.tar)\.enc', path.name) and stat.S_ISREG(path.lstat().st_mode):
        grant(path, 'r--')
        count += 1
print('Backup ACL ready; encrypted files:', count)

# Include the manifests and encrypted chunks produced by system snapshots.
for path in directory.glob('*.record.json'):
    if stat.S_ISREG(path.lstat().st_mode):
        grant(path, 'r--')
objects = directory / 'objects'
if objects.exists() and stat.S_ISDIR(objects.lstat().st_mode):
    grant(objects, 'r-x')
    for path in objects.rglob('*'):
        mode = path.lstat().st_mode
        if stat.S_ISDIR(mode):
            grant(path, 'r-x')
        elif stat.S_ISREG(mode):
            grant(path, 'r--')
