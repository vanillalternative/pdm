// Generates pdm/data/planos.json — the bundled registry of every Portuguese
// special planning instrument (planos/programas especiais) — from the verified
// research JSONs, with the adversarial-verification corrections applied.
import { readFileSync, writeFileSync } from 'node:fs';

const dir = new URL('.', import.meta.url);
const alb = JSON.parse(readFileSync(new URL('albufeiras.json', dir), 'utf8'));
const costa = JSON.parse(readFileSync(new URL('costa.json', dir), 'utf8'));
const prot = JSON.parse(readFileSync(new URL('protegidas.json', dir), 'utf8'));

// ---------- corrections from the adversarial verify pass (same as dossier) ----------
alb.items.push({
  name: 'POA da Albufeira do Alto Rabagão', kind: 'POAAP',
  diploma: 'Elaboração determinada: RCM n.º 141/2002, de 7 de dezembro',
  status: 'never approved — elaboration determined in 2002 but never concluded; DL 107/2009 regime applies. PEAAP determined in 2025 (Despacho n.º 10417/2025).',
  municipalities: ['Montalegre'],
});
for (const it of alb.items) {
  if (/Santa Clara/.test(it.name)) it.diploma += '; alterado por RCM n.º 56/2014, de 22 de setembro';
  if (/^POA da Albufeira do Alvito/.test(it.name)) it.notes = 'Revisão determinada por RCM n.º 106/2005 (nunca concluída).';
  if (/PEAFT/.test(it.name)) it.notes = 'Elaboração determinada pelo Despacho n.º 8097/2011; zonamento digital em SNIAmb/PAAP.';
  if (/Programa Especial da Albufeira da Caniçada/.test(it.name)) it.status += ' — prosseguimento: Despacho n.º 1369/2026, de 5 de fevereiro';
  if (/Programa Especial da Albufeira do Torrão/.test(it.name)) it.status += ' — prazo prorrogado 21 meses (Despacho n.º 1370/2026)';
  if (/Vilarinho das Furnas/.test(it.name) && it.kind === 'PEAAP') it.status += ' — prazo prorrogado 21 meses (Despacho n.º 1371/2026)';
  if (/Paradela, Salamonde e Venda Nova/.test(it.name)) it.status += ' — prorrogação Fev 2026 (Despacho n.º 1372/2026)';
  if (/^POACB/.test(it.name)) it.notes = 'Mai–jun 2026: RCM (reportada como n.º 113/2026) ratificou disposições do PDM de Abrantes incompatíveis com o POACB — estatuto em Abrantes parcialmente alterado; confirmar no DR.';
}
for (const it of costa.items) {
  if (/POOC Sintra-Sado/.test(it.name)) it.municipalities = ['Sintra', 'Cascais', 'Almada', 'Sesimbra', 'Setúbal'];
}
costa.items.push({
  name: 'POE Mondego — Programa de Ordenamento do Estuário do Mondego', kind: 'POE', diploma: '',
  status: 'NOT in force — on APA’s planned estuary-programs list; never approved.',
  municipalities: ['Figueira da Foz', 'Montemor-o-Velho'],
});
for (const it of prot.items) {
  if (/POPNDI/.test(it.name)) it.diploma = (it.diploma || '').replace('29 de julho', '28 de julho');
  if (/POPNSE/.test(it.name)) it.status = 'em vigor; excluído do lote de conversão de 2023 (incêndios de 2022) — coberto pelo Programa de Revitalização do PNSE (RCM n.º 40/2024); elaboração de PEPNSE determinada em separado (Despacho n.º 8124/2023).';
  if (/POPNSSM/.test(it.name)) { it.diploma = 'RCM n.º 77/2005, de 21 de março'; it.notes = 'Monforte pode não integrar a área de intervenção (RCM 77/2005) — verificar plantas oficiais.'; }
  if (/POPPAFCC/.test(it.name)) it.diploma = 'RCM n.º 178/2008, de 24 de novembro';
}

const islands = [
  { name: 'POCMAD — Programa para a Orla Costeira da Madeira', kind: 'POC', diploma: 'RCG Regional n.º 48/2024, de 2 de fevereiro; Modelo Territorial alterado mar 2026', status: 'em vigor', municipalities: ['Calheta (Madeira)', 'Ponta do Sol', 'Ribeira Brava', 'Câmara de Lobos', 'Funchal', 'Santa Cruz', 'Machico', 'Santana', 'São Vicente', 'Porto Moniz'] },
  { name: 'POOC Costa Norte da Ilha de São Miguel', kind: 'POOC', diploma: 'DRR n.º 6/2005/A', status: 'em vigor', municipalities: ['Ponta Delgada', 'Ribeira Grande', 'Nordeste'] },
  { name: 'POOC Costa Sul da Ilha de São Miguel', kind: 'POOC', diploma: 'DRR n.º 29/2007/A', status: 'em vigor', municipalities: ['Ponta Delgada', 'Lagoa (Açores)', 'Vila Franca do Campo', 'Povoação', 'Nordeste'] },
  { name: 'POOC Ilha Terceira', kind: 'POOC', diploma: 'DRR n.º 1/2005/A, substituído por DRR n.º 30/2023/A', status: 'em vigor', municipalities: ['Angra do Heroísmo', 'Praia da Vitória'] },
  { name: 'POOC Ilha de Santa Maria', kind: 'POOC', diploma: '(diploma não verificado)', status: 'em vigor', municipalities: ['Vila do Porto'] },
  { name: 'POOC Ilha Graciosa', kind: 'POOC', diploma: 'DRR n.º 13/2008/A', status: 'em vigor', municipalities: ['Santa Cruz da Graciosa'] },
  { name: 'POOC Ilha de São Jorge', kind: 'POOC', diploma: 'DRR n.º 24/2005/A, alterado por DRR n.º 2/2022/A', status: 'em vigor', municipalities: ['Velas', 'Calheta de São Jorge'] },
  { name: 'POOC Ilha do Pico', kind: 'POOC', diploma: 'DRR n.º 24/2011/A', status: 'em vigor', municipalities: ['Madalena', 'São Roque do Pico', 'Lajes do Pico'] },
  { name: 'POOC Ilha do Faial', kind: 'POOC', diploma: 'DRR n.º 19/2012/A', status: 'em vigor', municipalities: ['Horta'] },
  { name: 'POOC Ilha das Flores', kind: 'POOC', diploma: 'DRR n.º 24/2008/A', status: 'em vigor', municipalities: ['Santa Cruz das Flores', 'Lajes das Flores'] },
  { name: 'POOC Ilha do Corvo', kind: 'POOC', diploma: 'DRR n.º 14/2008/A', status: 'em vigor', municipalities: ['Corvo'] },
];

// ---------- helpers ----------
const STOP = new Set(['de', 'do', 'da', 'dos', 'das', 'e', 'd', 'a', 'o']);
const tokens = s => String(s ?? '')
  .toLowerCase()
  .normalize('NFD').replace(/[̀-ͯ]/g, '')
  .split(/[^a-z0-9]+/)
  .filter(w => w && !STOP.has(w));
// Subjects are stored as space-joined token sequences; the Go matcher requires
// the sequence to appear contiguously in the tokenized live detail, so short
// names ("Vilar") never substring-match longer ones ("Vilarinho das Furnas").
const squash = s => tokens(s).join(' ');

function state(status) {
  const s = status.toLowerCase();
  if (/revoked|revogad/.test(s)) return 'revogado';
  if (/never approved|nunca|não aprovado|not in force/.test(s)) return 'nunca-aprovado';
  if (/partially in force|parcial/.test(s) && /em vigor|in force/.test(s)) return 'parcial';
  if (/em vigor|in force|approved 8 june/.test(s)) return 'vigor';
  if (/elabora|determinada|awaiting|not started|a aguardar/.test(s)) return 'elaboracao';
  return 'vigor';
}

function slug(name) {
  return tokens(name).join('-').slice(0, 56).replace(/-+$/, '');
}

// Subject match keys: the proper names a live layer's attributes would carry.
function subjectsAlbufeira(name) {
  // "POA das Albufeiras do Touvedo e Alto Lindoso" → ["touvedo", "alto lindoso"].
  // Parentheticals are replaced by a space, not removed, so
  // "Santa Águeda (Marateca) e Pisco" splits cleanly instead of gluing tokens.
  const m = name.match(/(?:Albufeiras?|Açude) (?:de |do |da |dos |das )?(.+)$/);
  if (!m) return [];
  let core = m[1].replace(/\s*\(.*?\)\s*/g, ' ').replace(/ — .*$/, '');
  return core.split(/,| e /).map(s => squash(s)).filter(Boolean);
}
function subjectsCosta(name) {
  // Extract the troço: "POC-EO — Programa da Orla Costeira Espichel-Odeceixe"
  // → "Espichel-Odeceixe"; "POOC Burgau-Vilamoura — ..." → "Burgau-Vilamoura".
  let t = name;
  const i = name.lastIndexOf('Orla Costeira');
  if (i >= 0) t = name.slice(i + 'Orla Costeira'.length);
  else {
    const m = name.match(/POO?C(?:MAD)?(?:-\w+)?\s+([^—:]+)/);
    if (m) t = m[1];
  }
  t = t.replace(/^[\s—:–-]+/, '').trim();
  return [squash(t)].filter(Boolean);
}
function subjectsProt(item) {
  // Parse the canonical plan NAME first — protectedArea is sometimes a whole
  // descriptive sentence (POPNArr's mentions the marine park), which would
  // poison the match-key. Fall back to protectedArea only when the name
  // doesn't carry the protected-area designation.
  const fromName = item.name.replace(/^.*?—\s*/, '');
  let m = fromName.match(/(?:Parque Nacional|Parque Natural|Reserva Natural|Paisagem Protegida)\s+(?:de |do |da |dos |das )?(.+)$/i);
  if (!m && item.protectedArea) {
    m = item.protectedArea.match(/(?:Parque Nacional|Parque Natural|Reserva Natural|Paisagem Protegida)\s+(?:de |do |da |dos |das )?([^.,;(]+)/i);
  }
  const core = m ? m[1] : fromName;
  return [squash(core.replace(/\s*\(.*?\)\s*/g, ' '))].filter(Boolean);
}
function subjectsEstuario(name) {
  const m = name.match(/Estuário (?:de |do |da )?(\w+)/i);
  return m ? [squash('estuario ' + m[1])] : [];
}

// cleanMuni folds research annotations back to a plain CAOP municipality name:
// "(intended) Tejo estuary margins: Lisboa" → "Lisboa";
// "Espinho (Barrinha de Esmoriz)" → "Espinho". Island disambiguations like
// "Lagoa (Açores)" are kept — they must NOT collide with the mainland Lagoa.
const cleanMuni = m => m
  .replace(/^\(intended\)\s*/i, '')
  .replace(/^[^:()]*:\s*/, '')
  .replace(/\s*\(Barrinha de Esmoriz\)/, '')
  .trim();

const out = [];
function push(item, family, subjects) {
  if (/regime estatutário/.test(item.kind || '')) return; // covered by the albufeira layer's DL 107/2009 note
  out.push({
    id: slug(item.name),
    name: item.name,
    kind: (item.kind || '').split(' ')[0] || 'PEOT',
    family,
    diploma: item.diploma || '',
    status: item.status,
    state: state(item.status),
    municipalities: (item.municipalities || []).map(cleanMuni).filter(Boolean),
    notes: item.notes || '',
    subjects,
  });
}

for (const it of alb.items) push(it, 'albufeira', subjectsAlbufeira(it.name));
for (const it of costa.items) push(it, it.kind === 'POE' ? 'estuario' : 'costa', it.kind === 'POE' ? subjectsEstuario(it.name) : subjectsCosta(it.name));
for (const it of islands) push(it, 'costa', subjectsCosta(it.name));
for (const it of prot.items) {
  // PSRN2000 is a nationwide sectoral overlay, not a per-municipality special
  // plan — the live ZEC/ZPE layers carry Natura 2000; no registry row needed.
  if (/PSRN2000|Rede Natura/.test(it.name)) continue;
  push({ ...it, kind: it.name.startsWith('PEPNSAC') ? 'PEAP' : 'POAP' }, 'area-protegida', subjectsProt(it));
}

// State overrides where the heuristic misreads a compound status: POC-VVRSA's
// status mentions the old POOC "remains in force" (the POC itself is not);
// the São Domingos programa was abandoned, never approved (the revoked
// instrument is the separate POA entry); POC-OV is squarely em elaboração.
const stateOverrides = {
  'poc-vvrsa-programa-orla-costeira-vilamoura-vila-real-santo': 'elaboracao',
  'poc-ov-programa-orla-costeira-odeceixe-vilamoura': 'elaboracao',
  'programa-especial-albufeira-sao-domingos': 'nunca-aprovado',
};
for (const it of out) {
  for (const [prefix, st] of Object.entries(stateOverrides)) {
    if (it.id.startsWith(prefix.slice(0, 40))) it.state = st;
  }
}

// Manual subject aliases where official spellings diverge from live-layer attributes.
for (const it of out) {
  if (/castelo d[eo] bode/i.test(it.name)) it.subjects = [...new Set([...it.subjects, 'castelo bode'])];
  if (/alqueva/i.test(it.name)) it.subjects = [...new Set([...it.subjects, 'alqueva', 'pedrogao'])];
  if (/idanha/i.test(it.name) && it.family === 'albufeira') it.subjects = [...new Set([...it.subjects, 'idanha', 'marechal carmona'])];
  if (/bravura/i.test(it.name)) it.subjects = [...new Set([...it.subjects, 'bravura', 'odiaxere'])];
  if (/vale de gaio/i.test(it.name)) it.subjects = [...new Set([...it.subjects, 'vale gaio', 'trigo morais'])];
  if (/santa águeda/i.test(it.name)) it.subjects = [...new Set([...it.subjects, 'santa agueda', 'marateca', 'pisco'])];
}

// dedupe ids
const seen = new Map();
for (const it of out) {
  let id = it.id, n = 2;
  while (seen.has(id)) id = `${it.id}-${n++}`;
  seen.set(id, true);
  it.id = id;
}

const doc = {
  _source: {
    name: 'Registo de planos/programas especiais de ordenamento do território (PEOT) — compilado de APA, ICNF, DGT/PCGT e DRE',
    note: 'Inventário verificado (jul 2026): POAAP/PEAAP (albufeiras), POOC/POC (orla costeira), POE (estuários — nenhum aprovado), POAP/PEAP (áreas protegidas), PSRN2000. Estados voláteis — re-verificar PCGT/DRE periodicamente.',
    retrieved_at: '2026-07-06',
  },
  instruments: out,
};
writeFileSync(new URL('../planos.json', dir), JSON.stringify(doc, null, 1) + '\n');
console.log('instruments:', out.length);
const fam = {};
for (const it of out) fam[it.family] = (fam[it.family] || 0) + 1;
console.log(fam);
console.log('sample subjects:', out.filter(i => /castelo|sudoeste|espichel|burgau|foz tua/i.test(i.name)).map(i => `${i.name} => ${JSON.stringify(i.subjects)}`).join('\n'));
