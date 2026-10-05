export const transform = (asset) => ({
  x: asset.x,
  y: 0,
  z: asset.z,
  facing: asset.facing || 0,
  width: asset.width,
  height: asset.height,
  depth: asset.depth,
});
export const identity = (asset) => ({
  type: asset.type,
  location: asset.location,
  pod: asset.pod,
  serial: asset.serial || '',
});
export const status = (asset) => ({ value: asset.status, summary: asset.summary, age: 0 });
export const inventory = (asset) => ({
  components: asset.components || [],
  cables: asset.cables || [],
});
export const source = (asset, historical) => ({ sourceTime: asset.sourceTime || '', historical });
