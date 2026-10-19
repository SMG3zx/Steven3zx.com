// Candidate bytes are only accepted after official codec validation.
import fs from 'node:fs';
const schema = JSON.parse(fs.readFileSync('assets/inventory-schema-26.3.json','utf8'));
const fixture = JSON.parse(fs.readFileSync('assets/inventory-26.3.json','utf8'));
const types = schema.types, fields = types.SlotComponent[1][1].type[1].fields;
function encode(type, scope={}) {
  if (typeof type==='string') {
    const primitives = {void:[],varint:[0],bool:[0],f32:[0,0,0,0],f64:Array(8).fill(0),i32:Array(4).fill(0),UUID:Array(16).fill(0),anonymousNbt:[10,0],anonOptionalNbt:[0]};
    if(Object.hasOwn(primitives,type)) return primitives[type];
    if(type==='ItemStackTemplate') return [1,1,0,0];
    return encode(types[type],scope);
  }
  const [kind,args]=type;
  if(kind==='container') { let bytes=[]; const local={...scope}; for(const field of args){local[field.name]=0;bytes.push(...encode(field.type,local));} return bytes; }
  if(kind==='switch') return encode(args.fields?.[String(scope[args.compareTo]??0)]??args.default??'void',scope);
  if(kind==='mapper') return [Number(Object.keys(args.mappings)[0])];
  if(kind==='option') return [0];
  if(kind==='registryEntryHolder') return [1];
  if(kind==='registryEntryHolderSet') return [1];
  if(kind==='bitfield') return Array(8).fill(0);
  if(kind==='array') { const count=typeof args.count==='number'?args.count:0; return [...(args.countType?[count]:[]),...Array.from({length:count},()=>encode(args.type,scope)).flat()]; }
  if(kind==='pstring') {const text=Buffer.from('minecraft:stone');return [text.length,...text];}
  if(kind==='buffer') return [0];
  throw Error(`Unknown candidate operator ${kind}`);
}
const candidates={};
for(const component of fixture.components) {
  if(!Object.hasOwn(fixture.default_component_samples,component.name)) {
    candidates[component.name]=Buffer.from(encode(fields[component.name.replace(/^minecraft:/,'')])).toString('hex');
  }
}
candidates['minecraft:custom_name']='08000178';
candidates['minecraft:intangible_projectile']='0a00';
candidates['minecraft:profile']='0000000000000000';
// Persistent codec requires an explicit loot-table key.
candidates['minecraft:container_loot']='0a08000a6c6f6f745f7461626c65000f6d696e6563726166743a656d70747900';
fs.writeFileSync('target/component-candidates.json',JSON.stringify(candidates));
console.log(`Generated ${Object.keys(candidates).length} candidates for official validation`);
