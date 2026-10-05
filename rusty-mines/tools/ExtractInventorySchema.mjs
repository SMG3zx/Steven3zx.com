// Extract only the transitive inventory schema, pinned to an audited source.
// Run: node tools/ExtractInventorySchema.mjs target/protocol-26.3-reference.json
import fs from 'node:fs';
import {createHash} from 'node:crypto';
const sourceBytes=fs.readFileSync(process.argv[2]);
const hash=createHash('sha256').update(sourceBytes).digest('hex');
if(hash!=='0cf80627d03ef23576c578b51c07dad2f7928a83ebd4891b46258d1e528f2837')throw Error('Reference schema hash mismatch');
const source = JSON.parse(sourceBytes.toString('utf8'));
const official = JSON.parse(fs.readFileSync('assets/inventory-26.3.json', 'utf8'));
const mappings = source.types.SlotComponentType[1].mappings;
// Corrections from pinned official 26.3 javap codec compositions. The reference
// registry contains newly added IDs whose payload definitions were incomplete.
const fields = source.types.SlotComponent[1][1].type[1].fields;
const container = (...fields) => ['container', fields.map(([name,type]) => ({name,type}))];
const either = type => container(['constant','bool'], ['value',['switch',{compareTo:'constant',fields:{true:type,false:'string'}}]]);
const swing = container(['type',['mapper',{type:'varint',mappings:{0:'none',1:'whack',2:'stab'}}]], ['duration','varint']);
Object.assign(fields, {
  attack_animation:swing, interact_animation:swing, block_transformer:'varint',
  villager_food:container(['nutrition','varint']), compostable:container(['chance',either('i32')]),
  cooking_fuel:container(['burn_duration',either('i32')],['remainder_chance',either('f32')]),
  brewing_fuel:container(['operations',either('i32')],['remainder_chance',either('f32')]),
  mob_visibility:container(['entities','IDSet'],['multiplier','f32']),
  provides_pottery_pattern:'varint', waxed:'void', 'cushion/color':'varint',
  sign_text_front:container(['messages',['array',{count:4,type:'anonymousNbt'}]],
    ['filtered',['option',['array',{count:4,type:'anonymousNbt'}]]],['color','varint'],['glowing','bool']),
});
fields.sign_text_back = fields.sign_text_front;
fields.intangible_projectile = 'anonymousNbt';
fields.use_remainder = 'ItemStackTemplate';
fields.damage_resistant = container(['types','IDSet']);
fields.provides_banner_patterns = 'IDSet';
fields.damage_type = ['registryEntryHolder',{baseName:'id',otherwise:{name:'data',type:'DamageTypeData'}}];
fields.jukebox_playable = ['registryEntryHolder',{baseName:'id',otherwise:{name:'data',type:'JukeboxSongData'}}];
fields.provides_trim_material = ['registryEntryHolder',{baseName:'id',otherwise:{name:'data',type:'ArmorTrimMaterial'}}];
fields.pot_decorations = ['array',{count:4,type:['option','ItemStackTemplate']}];
fields.blocks_attacks[1].find(field=>field.name==='bypassedBy').type = ['option','IDSet'];
for (const name of Object.values(mappings)) if (!Object.hasOwn(fields,name)) throw Error(`Missing component schema: ${name}`);
for (const component of official.components) {
  if (mappings[component.id] !== component.name.replace(/^minecraft:/, '')) throw Error('Component registry mismatch');
}
const types = {};
function visit(type) {
  if (typeof type === 'string') {
    if (type === 'native' || Object.hasOwn(types, type)) return;
    if (!Object.hasOwn(source.types, type)) throw Error(`Missing type: ${type}`);
    types[type] = source.types[type]; visit(types[type]); return;
  }
  if (!Array.isArray(type)) return;
  const [kind, args] = type;
  if (kind === 'container') for (const field of args) visit(field.type);
  else if (kind === 'switch') { for (const value of Object.values(args.fields ?? {})) visit(value); if (args.default) visit(args.default); }
  else if (kind === 'option') visit(args);
  else if (['array', 'mapper', 'pstring', 'buffer'].includes(kind)) { if (args.type) visit(args.type); if (args.countType) visit(args.countType); }
  else if (kind === 'registryEntryHolder') visit(args.otherwise.type);
  else if (kind === 'registryEntryHolderSet') visit(args.base.type);
  else if (kind !== 'bitfield') throw Error(`Unreviewed schema operator: ${kind}`);
}
for (const name of ['Slot', 'UntrustedSlot', 'HashedSlot']) visit(name);
const asset = {version:'26.3', protocol:777, server_sha1:official.server_sha1,
  source:'https://github.com/wupengabc/node-minecraft-data', commit:'9c3d5857616620b3c8b30c23631374dc874670a3', reference_sha256:hash, types};
fs.writeFileSync('assets/inventory-schema-26.3.json', JSON.stringify(asset) + '\n');
console.log(`Extracted ${Object.keys(types).length} inventory types, all ${Object.keys(mappings).length} component schemas`);
console.log('Native types:', Object.entries(types).filter(([,value]) => value === 'native').map(([key]) => key));
