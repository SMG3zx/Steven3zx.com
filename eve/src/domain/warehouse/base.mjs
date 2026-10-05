// Static physical envelope. ADAM snapshots populate contents and state only.
export const BASE_RACK = Object.freeze({ width: 0.8, height: 2.2, depth: 1.2, slotCapacity: 48 });
export const BASE_POD = Object.freeze({ slotCapacity: BASE_RACK.slotCapacity, aisleWidth: 4.1 });
export function createPodBase(code, locations = []) {
  return {
    code,
    slotCapacity: Math.max(BASE_POD.slotCapacity, locations.length),
    locations: [...locations].sort(),
  };
}
