import assert from 'node:assert/strict';
import test from 'node:test';
import { selectDevice } from './select-device.mjs';

const phone = (name = 'Speedy', id = 'phone-udid') => ({
  identifier: `core-${id}`,
  properties: {
    hardware: { udid: id, deviceType: 'iPhone', reality: 'physical' },
    connection: { transportType: 'localNetwork', pairingState: 'paired', state: 'disconnected' },
    state: { name },
  },
});
const list = (...devices) => ({ result: { devices } });

test('selects a paired Wi-Fi phone whose development tunnel is not connected yet', () => {
  assert.deepEqual(selectDevice(list(phone()), '', true), { id: 'phone-udid', name: 'Speedy' });
});

test('ignores booted simulators and paired watches', () => {
  const simulator = phone('Simulator', 'sim');
  simulator.properties.hardware.reality = 'simulated';
  simulator.properties.connection.state = 'connected';
  const watch = phone('Watch', 'watch');
  watch.properties.hardware.deviceType = 'appleWatch';
  assert.equal(selectDevice(list(simulator, watch, phone())).id, 'phone-udid');
});

test('accepts a name with spaces, UDID, or CoreDevice identifier and returns the hardware UDID for xcodebuild', () => {
  const device = phone('My iPhone');
  for (const selector of ['My iPhone', 'phone-udid', 'core-phone-udid']) {
    assert.equal(selectDevice(list(device), selector).id, 'phone-udid');
  }
});

test('supports older Xcode JSON and wired device launches', () => {
  const legacy = {
    identifier: 'core-old',
    hardwareProperties: { udid: 'old-udid', deviceType: 'iPhone', reality: 'physical' },
    deviceProperties: { name: 'Old iPhone' },
    connectionProperties: { transportType: 'wired', pairingState: 'paired', tunnelState: 'connected' },
  };
  assert.equal(selectDevice(list(phone(), legacy)).id, 'old-udid');
  assert.equal(selectDevice(list(phone(), legacy), '', true).id, 'phone-udid');
  assert.throws(() => selectDevice(list(legacy), 'Old iPhone', true), /No paired Wi-Fi/);
});

test('requires an explicit selection when multiple phones are eligible', () => {
  assert.throws(() => selectDevice(list(phone(), phone('Another', 'other'))), /LOLA_DEVICE/);
  assert.equal(selectDevice(list(phone(), phone('Another', 'other')), 'Another').id, 'other');
});

test('rejects unpaired, unknown, missing, and invalid destinations', () => {
  const unpaired = phone();
  unpaired.properties.connection.pairingState = 'unpaired';
  assert.throws(() => selectDevice(list(unpaired)), /No paired/);
  assert.throws(() => selectDevice(list(phone()), 'missing'), /No paired/);
  assert.throws(() => selectDevice(list()), /No paired/);
  assert.throws(() => selectDevice({}), /Invalid/);
});
