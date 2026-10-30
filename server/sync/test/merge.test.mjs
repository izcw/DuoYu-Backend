import test from 'node:test';
import assert from 'node:assert/strict';
import { merge } from '../src/store.mjs';
test('identical concurrent layouts are accepted', () => {
  const base = {
    id: 'a',
    pageId: 'home',
    parent: '',
    gridX: 0,
    gridY: 0,
    width: 1,
    height: 1,
    order: 0,
  };
  const value = { ...base, gridX: 1 };
  assert.deepEqual(
    merge(
      { table: 'libraryItems', base, value, revision: '1' },
      { payload: value, revision: '2', deleted: false },
    ).payload,
    value,
  );
});
test('fresh create cannot reuse a deleted entity ID', () => {
  const value = { id: 'a', name: 'one' };
  assert.throws(
    () =>
      merge(
        { table: 'libraryPages', base: null, value, revision: '0' },
        { payload: value, revision: '2', deleted: true },
      ),
    (e) => e.code === 'deleted_or_missing',
  );
});
test('icon color and text edits merge independently', () => {
  const b = { mode: 'text', text: '前', color: '#fff', material: 'glass' };
  const icon = (p) => 'duoyu-icon:' + JSON.stringify(p);
  const value = merge(
    {
      table: 'libraryItems',
      revision: '1',
      base: { id: 'i', icon: icon(b) },
      value: { id: 'i', icon: icon({ ...b, text: '新' }) },
    },
    {
      revision: '2',
      payload: { id: 'i', icon: icon({ ...b, color: '#000' }) },
      deleted: false,
    },
  );
  const p = JSON.parse(value.payload.icon.slice(11));
  assert.equal(p.text, '新');
  assert.equal(p.color, '#000');
  assert.throws(
    () =>
      merge(
        {
          table: 'libraryItems',
          revision: '1',
          base: { id: 'i', icon: icon(b) },
          value: { id: 'i', icon: icon({ ...b, text: 'A' }) },
        },
        {
          revision: '2',
          payload: { id: 'i', icon: icon({ ...b, text: 'B' }) },
          deleted: false,
        },
      ),
    (e) => e.code === 'field_conflict',
  );
});
test('fixed settings require an explicit current-revision restore', () => {
  const payload = { id: 'glassMode', value: 'soft' },
    remote = { revision: '3', payload, deleted: true };
  assert.throws(
    () =>
      merge(
        { table: 'preferences', revision: '0', base: null, value: payload },
        remote,
      ),
    (e) => e.code === 'deleted_or_missing',
  );
  assert.equal(
    merge(
      {
        table: 'preferences',
        revision: '3',
        base: payload,
        value: payload,
        restore: true,
      },
      remote,
    ).deleted,
    false,
  );
});
