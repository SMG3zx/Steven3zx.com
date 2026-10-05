// Reference compositions derived from NVIDIA's GB300 NVL72 architecture.
// Quantities are per rack; ADAM remains authoritative for which observed
// slots are present and their state.
export const RACK_COMPOSITIONS = Object.freeze({
  GB300_NVL72: Object.freeze({
    computeTrays: 18,
    nvswitchTrays: 9,
    nvswitchAsicsPerTray: 2,
    powerShelves: 8,
    psusPerPowerShelf: 6,
    computeTray: Object.freeze({
      graceCpus: 2,
      blackwellGpus: 4,
      connectX8MezzanineBoards: 2,
      connectX8Adapters: 4,
      blueField3Dpu: 1,
      m2Nvme: 1,
      e1sNvme: 4,
    }),
  }),
  MGX: Object.freeze({
    chassis: 1,
    fanWalls: 3,
    cableLooms: ['power', 'management', 'fabric'],
  }),
});

export function rackComposition(model = '', subModel = '', kind = '') {
  const identity = `${model} ${subModel} ${kind}`.toUpperCase();
  return identity.includes('MGX') ? RACK_COMPOSITIONS.MGX : RACK_COMPOSITIONS.GB300_NVL72;
}
