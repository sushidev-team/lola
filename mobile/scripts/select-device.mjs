import { readFileSync } from 'node:fs';
import { fileURLToPath } from 'node:url';

// Xcode 27 groups these fields under properties; older versions use *Properties.
// A paired Wi-Fi phone may have a disconnected tunnel until Xcode contacts it.
export function selectDevice(data, selector = '', wireless = false) {
  if (!Array.isArray(data.result?.devices)) throw new Error('Invalid devicectl device list.');
  const devices = data.result.devices.map(device => {
    const hardware = device.properties?.hardware ?? device.hardwareProperties ?? {};
    const connection = device.properties?.connection ?? device.connectionProperties ?? {};
    const state = device.properties?.state ?? device.deviceProperties ?? {};
    return {
      id: hardware.udid,
      coreDeviceID: device.identifier,
      name: state.name,
      hardware,
      connection,
      connected: (connection.state ?? connection.tunnelState) === 'connected',
    };
  }).filter(device =>
    ['iPhone', 'iPad'].includes(device.hardware.deviceType)
    && device.hardware.reality !== 'simulated'
    && device.connection.transportType !== 'sameMachine'
    && device.connection.pairingState === 'paired'
    && typeof device.id === 'string' && device.id.length > 0
    && typeof device.name === 'string'
    && !/[\r\n]/.test(device.id + device.name)
    && (!wireless || device.connection.transportType === 'localNetwork')
  );
  let matches = selector
    ? devices.filter(device => [device.id, device.coreDeviceID, device.name].includes(selector))
    : devices;
  if (!selector && matches.some(device => device.connected)) {
    matches = matches.filter(device => device.connected);
  }
  if (matches.length === 0) {
    throw new Error(`No paired ${wireless ? 'Wi-Fi ' : ''}iPhone/iPad${selector ? ` matching "${selector}"` : ''}.`);
  }
  if (matches.length > 1) {
    throw new Error(`Multiple devices match; set LOLA_DEVICE to a UDID:\n${matches.map(device => `  ${device.name}: ${device.id}`).join('\n')}`);
  }
  return { id: matches[0].id, name: matches[0].name };
}

if (process.argv[1] === fileURLToPath(import.meta.url)) {
  try {
    const device = selectDevice(JSON.parse(readFileSync(process.argv[2], 'utf8')), process.argv[3], process.argv[4] === '1');
    console.log(`${device.id}\n${device.name}`);
  } catch (error) {
    console.error(`run-device: ${error.message}`);
    process.exitCode = 1;
  }
}
