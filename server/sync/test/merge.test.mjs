import test from 'node:test';
import assert from 'node:assert/strict';
import { merge, validateGraph, validateOperation } from '../src/store.mjs';
import { randomUUID } from 'node:crypto';

test('memo sync validates records, separates content from layout and keeps rich text consistent', () => {
  const page = { id: 'ideas', name: '灵感', order: 0, icon: 'Notes' };
  const base = {
    id: 'note', title: '', body: 'word', html: '<b>word</b>', pageId: 'ideas', color: 'rose',
    x: -20, y: 50, width: 264, height: 212, layer: 1, archivedAt: null,
    archiveOrder: 0, createdAt: 1, updatedAt: 2,
  };
  const entities = [{ table: 'memoPages', id: page.id, payload: page }, { table: 'memos', id: base.id, payload: base }];
  const create = entities.map(e => ({ table: e.table, id: e.id, base: null, value: e.payload, revision: '0' }));
  validateOperation({ id: randomUUID(), deviceId: randomUUID(), changes: create });
  validateGraph(entities);
  assert.throws(() => validateGraph(entities.slice(1)), e => e.code === 'missing_page');
  for (const invalid of [{ color: 'no' }, { archivedAt: false }, { width: 0 }, { body: 4 }, { updatedAt: 1e20 }])
    assert.throws(() => validateGraph([entities[0], { ...entities[1], payload: { ...base, ...invalid } }]), e => e.code === 'invalid_memo');
  const change = { table: 'memos', revision: '2', base, value: { ...base, body: 'new', html: '<b>new</b>' } };
  const remote = { revision: '3', payload: { ...base, x: 75 } };
  assert.deepEqual(merge(change, remote).payload, { ...change.value, x: 75 });
  assert.throws(() => merge(change, { ...remote, payload: { ...base, html: '<u>word</u>' } }), e => e.code === 'field_conflict');
});
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
test('workspace undo requires matching deleted content and revision', () => {
  for (const table of ['tabCollections', 'savedTabs']) {
    const payload = { id: 'item', name: 'original' };
    const remote = { revision: '5', payload, deleted: true };
    const restore = { table, revision: '5', base: payload, value: payload, restore: true };
    assert.deepEqual(merge(restore, remote), { deleted: false, payload });
    assert.deepEqual(merge({ ...restore, restore: false }, remote), remote);
    for (const change of [{ restore: false, base: null }, { revision: '4' }, { base: { ...payload, name: 'changed' } }])
      assert.throws(() => merge({ ...restore, ...change }, remote), e => e.code === 'deleted_or_missing');
  }
});
