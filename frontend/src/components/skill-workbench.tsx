import { useEffect, useState, type ReactNode } from "react";
import { Button } from "@/components/ui/button";
import { Input } from "@/components/ui/input";
import { Textarea } from "@/components/ui/textarea";
import { type ActivityNode } from "@/lib/api/client";
import { KIND_LABELS, skillKey, skillGaps, loadSkillModel, loadSkillNodes, saveSkillModel, extractSkills, compareSkills, validateSkillModel, type SkillModel, type SkillDefinition, type SkillTool, type SkillLink, type SkillComparison } from "@/lib/skill-model";
const blankDefinition = (): SkillDefinition => ({ id: crypto.randomUUID(), version: 1, title: "", kind: "knowledge", action: "", object: "", conditions: "", result: "", criterion: "", autonomy: "", scope: "", tools: [] });
const blankTool = (): SkillTool => ({ id: crypto.randomUUID(), name: "", kind: "application", aliases: [] });
function Field({ title, children }: {
    title: string;
    children: ReactNode;
}) { return <label className="grid gap-1 text-sm"><span>{title}</span>{children}</label>; }
const selectClass = "h-9 w-full rounded-md border bg-background px-2 text-sm";
const statusLabel: Record<string, string> = { proposed: "Не проверено", approved: "Подтверждено методистом", rejected: "Отклонено" };
const basisLabel: Record<string, string> = { requirement: "Требование источника", interview: "Рассказ специалиста", interpretation: "Методическая интерпретация" };
function describe(error: unknown) { return error instanceof Error ? error.message : "Не удалось выполнить действие"; }
export function SkillWorkbench({ runId, readOnly = false }: {
    runId: string;
    readOnly?: boolean;
}) {
 return <SkillWorkbenchEditor key={runId} runId={runId} readOnly={readOnly}/>;
}
function SkillWorkbenchEditor({runId,readOnly}:{runId:string;readOnly:boolean}) {
    const [model, setModel] = useState<SkillModel | null>(null);
    const [savedKeys, setSavedKeys] = useState<Set<string>>(new Set());
    const [nodes, setNodes] = useState<ActivityNode[]>([]);
    const [busy, setBusy] = useState(false), [error, setError] = useState("");
    const [dirty, setDirty] = useState(false), [historical, setHistorical] = useState(false), [latest, setLatest] = useState(0);
    const [definition, setDefinition] = useState<SkillDefinition>(blankDefinition);
    const [tool, setTool] = useState<SkillTool>(blankTool);
    const [query, setQuery] = useState(""), [toolFilter, setToolFilter] = useState("");
    const [linkNode, setLinkNode] = useState(""), [linkDefinition, setLinkDefinition] = useState(""), [evidenceIds, setEvidenceIds] = useState<string[]>([]);
    const [basis, setBasis] = useState("requirement"), [reason, setReason] = useState("");
    const [expected, setExpected] = useState(""), [actual, setActual] = useState(""), [comparison, setComparison] = useState<SkillComparison | null>(null);
    const editable = !readOnly && !historical && !busy;
    useEffect(() => {
        let cancelled = false;
        Promise.all([loadSkillModel(runId), loadSkillNodes(runId)]).then(([m, n]) => { if (!cancelled) {
            setModel(m);
            setSavedKeys(new Set(m.definitions.map(skillKey)));
            setLatest(m.revision);
            setNodes(n);
        } }).catch(e => { if (!cancelled)
            setError(describe(e)); });
        return () => { cancelled = true; };
    }, [runId]);
    function change(next: SkillModel) { setModel(next); setDirty(true); setComparison(null); setError(""); }
    async function importDictionary(file:File) {
      if(!model)return;setBusy(true);setError("");
      try {
        if(file.size>2*1024*1024)throw new Error("Файл словаря больше 2 МБ");
        const imported=JSON.parse(await file.text()) as SkillModel;
        if(!Array.isArray(imported.tools)||!Array.isArray(imported.definitions))throw new Error("В файле нет словаря инструментов и определений ЗУН");
        const tools=model.tools.map(t=>({...t,aliases:[...t.aliases]})),definitions=[...model.definitions];
        for(const t of imported.tools){const existing=tools.find(item=>item.id===t.id);if(existing){if(existing.name!==t.name||existing.kind!==t.kind)throw new Error("Конфликт ID инструмента");existing.aliases=Array.from(new Set([...existing.aliases,...t.aliases]));}else tools.push(t);}
        for(const d of imported.definitions){const index=definitions.findIndex(item=>item.id===d.id&&item.version===d.version);if(index>=0)definitions[index]=d;else definitions.push(d);}
        change(await validateSkillModel(runId,{...model,tools,definitions}));
      }catch(e){setError(describe(e));}finally{setBusy(false);}
    }
    async function persist() { if (!model)
        return; setBusy(true); setError(""); try {
        const saved = await saveSkillModel(runId, model);
        setModel(saved);
        setSavedKeys(new Set(saved.definitions.map(skillKey)));
        setLatest(saved.revision);
        setDirty(false);
        setNodes(await loadSkillNodes(runId));
    }
    catch (e) {
        setError(describe(e));
    }
    finally {
        setBusy(false);
    } }
    async function loadRevision(revision: number) { setBusy(true); setError(""); try {
        const m = await loadSkillModel(runId, revision || undefined);
        setModel(m);
        setSavedKeys(new Set(m.definitions.map(skillKey)));
        setNodes(await loadSkillNodes(runId));
        setHistorical(!!revision);
        if (!revision)
            setLatest(m.revision);
        setDirty(false);
        setComparison(null);
    }
    catch (e) {
        setError(describe(e));
    }
    finally {
        setBusy(false);
    } }
    async function extract() { setBusy(true); setError(""); try {
        change(await extractSkills(runId));
    }
    catch (e) {
        setError(describe(e));
    }
    finally {
        setBusy(false);
    } }
    async function compare() { if (!model)
        return; const a = model.definitions.find(d => skillKey(d) === expected), b = model.definitions.find(d => skillKey(d) === actual); if (!a || !b)
        return; setBusy(true); setError(""); try {
        setComparison(await compareSkills(runId, a, b));
    }
    catch (e) {
        setError(describe(e));
    }
    finally {
        setBusy(false);
    } }
    if (!model)
        return <div className="rounded-lg border p-4">{error ? <p role="alert">{error}</p> : "Загрузка ЗУН и инструментов…"}</div>;
    const currentNode = nodes.find(n => n.id === linkNode);
    const versions = new Map<string, number>();
    for (const d of model.definitions)
        versions.set(d.id, Math.max(versions.get(d.id) ?? 0, d.version));
    const toolNames = new Map(model.tools.map(t => [t.id, t.name]));
    const filtered = model.definitions.filter(d => d.version === versions.get(d.id) && (!toolFilter || d.tools.some(t => t.tool_id === toolFilter)) && `${d.title} ${d.action} ${d.object} ${d.tools.map(use => { const t = model.tools.find(t => t.id === use.tool_id); return t ? `${t.name} ${t.aliases.join(" ")}` : ""; }).join(" ")}`.toLocaleLowerCase().includes(query.toLocaleLowerCase()));
    function exportModel() { const url = URL.createObjectURL(new Blob([JSON.stringify(model, null, 2)], { type: "application/json" })); const a = document.createElement("a"); a.href = url; a.download = `zun-${runId}-v${model!.revision}.json`; a.click(); setTimeout(() => URL.revokeObjectURL(url), 1000); }
    return <section className="space-y-5" aria-label="ЗУН и инструменты">
  <div className="space-y-2"><h2 className="text-lg font-semibold">ЗУН и инструменты</h2><p className="text-sm text-muted-foreground">Опишите требование, способ применения инструментов и его основания. Соответствие описаний и решение методиста не являются оценкой специалиста.</p>
   <div className="flex flex-wrap items-center gap-2"><span className="text-sm">Версия словаря: {model.revision}{dirty ? " · есть несохранённые изменения" : ""}</span>
   <select aria-label="История словаря" className={selectClass + " max-w-48"} value={historical ? model.revision : 0} disabled={busy || dirty} onChange={e => void loadRevision(Number(e.target.value))}><option value={0}>Текущая версия</option>{Array.from({ length: latest }, (_, i) => latest - i).map(v => <option key={v} value={v}>Версия {v}</option>)}</select>
   {!readOnly && !historical && <><Button disabled={!editable || !dirty} onClick={() => void persist()}>Сохранить словарь</Button><Button variant="outline" disabled={!editable || dirty} onClick={() => void extract()}>Извлечь предложения из дерева</Button></>}
   <Button variant="outline" disabled={busy || dirty} onClick={() => void loadRevision(0)}>Загрузить текущую версию</Button><Button variant="outline" onClick={exportModel}>Экспорт</Button></div>
   {!readOnly&&!historical&&<Field title="Импортировать инструменты и определения из другого словаря"><Input type="file" accept="application/json,.json" disabled={!editable||dirty} onChange={e=>{const file=e.target.files?.[0];if(file)void importDictionary(file);e.target.value="";}}/></Field>}
   {historical && <p className="text-sm">Просмотр сохранённой версии. Для редактирования выберите текущую.</p>}
   {error && <p role="alert" className="text-sm text-destructive">{error}</p>}
  </div>
  <div className="grid gap-3 sm:grid-cols-2"><Field title="Поиск ЗУН или инструмента"><Input value={query} onChange={e => setQuery(e.target.value)}/></Field><Field title="Инструмент"><select className={selectClass} value={toolFilter} onChange={e => setToolFilter(e.target.value)}><option value="">Все инструменты</option>{model.tools.map(t => <option key={t.id} value={t.id}>{t.name}</option>)}</select></Field></div>
  <details className="rounded-lg border p-4"><summary className="cursor-pointer font-medium">Справочник инструментов ({model.tools.length})</summary><div className="mt-3 grid gap-3 sm:grid-cols-2">{model.tools.map(t=><article className="space-y-2 rounded border p-3" key={t.id}><button className="font-medium underline" onClick={()=>setToolFilter(t.id)}>{t.name}</button><p className="text-xs">Связанных определений: {model.definitions.filter(d=>d.version===versions.get(d.id)&&d.tools.some(u=>u.tool_id===t.id)).length}</p><Field title={`Синонимы ${t.name}`}><Input disabled={!editable} value={t.aliases.join(",")} onChange={e=>change({...model,tools:model.tools.map(item=>item.id===t.id?{...item,aliases:e.target.value?e.target.value.split(","):[]}:item)})}/></Field></article>)}</div></details>
  <div className="grid gap-3 lg:grid-cols-2">{filtered.map(d => <article className="space-y-2 rounded-lg border p-4" key={skillKey(d)}><div className="flex items-start justify-between gap-2"><h3 className="font-medium">{d.title}</h3><span className="text-xs">{KIND_LABELS[d.kind]} · v{d.version}</span></div>
   <dl className="grid gap-1 text-sm">{([['action', 'Действие'], ['object', 'Объект'], ['conditions', 'Условия'], ['result', 'Результат'], ['criterion', 'Проверка'], ['autonomy', 'Самостоятельность'], ['scope', 'Диапазон ситуаций']] as const).map(([key, label]) => <div key={key}><dt className="inline text-muted-foreground">{label}: </dt><dd className="inline">{d[key] || "не установлено"}</dd></div>)}</dl>
   <ul className="space-y-1 text-sm">{d.tools.map(t => <li key={t.tool_id}><button className="underline" onClick={() => setToolFilter(t.tool_id)}>{toolNames.get(t.tool_id)}</button>{t.version ? ` ${t.version}` : ""}: {t.usage} ({t.required ? "обязателен" : "пример применения"})</li>)}</ul>
   {skillGaps(d).length > 0 && <p className="text-xs text-muted-foreground">Нужно уточнить: {skillGaps(d).join(", ")}</p>}
   {editable && <Button variant="outline" onClick={() => { if (!savedKeys.has(skillKey(d)))
            change({ ...model, definitions: model.definitions.filter(item => skillKey(item) !== skillKey(d)) }); setDefinition({ ...d, version: savedKeys.has(skillKey(d)) ? (versions.get(d.id) ?? 0) + 1 : d.version, tools: d.tools.map(t => ({ ...t })) }); }}> {savedKeys.has(skillKey(d)) ? "Создать новую версию" : "Редактировать черновик"}</Button>}
  </article>)}</div>
  {filtered.length === 0 && <p className="text-sm text-muted-foreground">Определений по этому запросу пока нет.</p>}
  {!readOnly && !historical && <>
  <details className="rounded-lg border p-4"><summary className="cursor-pointer font-medium">Добавить инструмент</summary><form className="mt-3 grid gap-3 sm:grid-cols-2" onSubmit={e => { e.preventDefault(); change({ ...model, tools: [...model.tools, tool] }); setTool(blankTool()); }}>
   <Field title="Название"><Input required maxLength={300} value={tool.name} onChange={e => setTool({ ...tool, name: e.target.value })}/></Field><Field title="Тип"><select className={selectClass} value={tool.kind} onChange={e => setTool({ ...tool, kind: e.target.value })}>{Object.entries({ language: "Язык", database: "СУБД", framework: "Фреймворк", library: "Библиотека", platform: "Платформа", application: "Приложение", other: "Другое" }).map(([id, label]) => <option key={id} value={id}>{label}</option>)}</select></Field>
   <Field title="Синонимы через запятую"><Input value={tool.aliases.join(",")} onChange={e => setTool({ ...tool, aliases: e.target.value ? e.target.value.split(",") : [] })}/></Field><Button disabled={!editable} type="submit">Добавить в словарь</Button></form></details>
  <details open className="rounded-lg border p-4"><summary className="cursor-pointer font-medium">Определение ЗУН {definition.version > 1 ? `· новая версия ${definition.version}` : ""}</summary><form className="mt-3 grid gap-3 sm:grid-cols-2" onSubmit={e => { e.preventDefault(); if (model.definitions.some(d => skillKey(d) === skillKey(definition))) {
            setError("Эта версия уже добавлена. Создайте следующую версию.");
            return;
        } change({ ...model, definitions: [...model.definitions, definition] }); setDefinition(blankDefinition()); }}>
   <Field title="Название"><Input required maxLength={2000} value={definition.title} onChange={e => setDefinition({ ...definition, title: e.target.value })}/></Field><Field title="Вид"><select className={selectClass} value={definition.kind} onChange={e => setDefinition({ ...definition, kind: e.target.value as SkillDefinition['kind'] })}>{Object.entries(KIND_LABELS).map(([id, label]) => <option key={id} value={id}>{label}</option>)}</select></Field>
   <p className="text-xs text-muted-foreground sm:col-span-2">Знание проверяется объяснением; умение — выполнением действия; владение требует указать устойчивость и самостоятельность в заданных ситуациях. Неуточнённые поля оставьте пустыми.</p>
   {([['action', 'Действие / способ демонстрации знания'], ['object', 'Объект / предмет знания'], ['conditions', 'Условия'], ['result', 'Ожидаемый результат'], ['criterion', 'Критерий проверки'], ['autonomy', 'Самостоятельность и допустимая помощь'], ['scope', 'Диапазон ситуаций и устойчивость']] as const).map(([key, label]) => <Field key={key} title={label}><Textarea maxLength={2000} value={definition[key]} onChange={e => setDefinition({ ...definition, [key]: e.target.value })}/></Field>)}
   <fieldset className="space-y-2 sm:col-span-2"><legend className="text-sm font-medium">Применение инструментов</legend>{definition.tools.map((use, i) => <div key={use.tool_id} className="grid items-end gap-2 rounded border p-2 sm:grid-cols-4"><span>{toolNames.get(use.tool_id)}</span><Field title="Назначение"><Input required value={use.usage} onChange={e => setDefinition({ ...definition, tools: definition.tools.map((t, j) => j === i ? { ...t, usage: e.target.value } : t) })}/></Field><Field title="Версия, если существенна"><Input value={use.version ?? ""} onChange={e => setDefinition({ ...definition, tools: definition.tools.map((t, j) => j === i ? { ...t, version: e.target.value } : t) })}/></Field><div className="space-y-2"><label className="flex gap-2 text-sm"><input type="checkbox" checked={use.required} onChange={e => setDefinition({ ...definition, tools: definition.tools.map((t, j) => j === i ? { ...t, required: e.target.checked } : t) })}/>Обязателен</label><Button type="button" variant="outline" onClick={() => setDefinition({ ...definition, tools: definition.tools.filter((_, j) => j !== i) })}>Убрать</Button></div></div>)}
   <select aria-label="Добавить инструмент к ЗУН" className={selectClass} value="" onChange={e => { if (e.target.value)
            setDefinition({ ...definition, tools: [...definition.tools, { tool_id: e.target.value, usage: "", required: true }] }); }}><option value="">Выберите инструмент для добавления</option>{model.tools.filter(t => !definition.tools.some(u => u.tool_id === t.id)).map(t => <option key={t.id} value={t.id}>{t.name}</option>)}</select></fieldset>
   <div className="flex gap-2"><Button type="submit" disabled={!editable}>Добавить определение</Button><Button type="button" variant="outline" disabled={!editable} onClick={() => setDefinition(blankDefinition())}>Очистить форму</Button></div>
  </form></details>
  <details className="rounded-lg border p-4"><summary className="cursor-pointer font-medium">Связать ЗУН с задачей и цитатами</summary><form className="mt-3 grid gap-3" onSubmit={e => { e.preventDefault(); const d = model.definitions.find(d => skillKey(d) === linkDefinition); if (!d || !evidenceIds.length) {
            setError("Выберите определение и цитаты");
            return;
        } const link: SkillLink = { id: crypto.randomUUID(), node_id: linkNode, definition_id: d.id, version: d.version, evidence_ids: evidenceIds, evidence: [], basis, reason, status: "proposed" }; change({ ...model, links: [...model.links, link] }); setEvidenceIds([]); setReason(""); }}>
   <Field title="ЗУН"><select required className={selectClass} value={linkDefinition} onChange={e => setLinkDefinition(e.target.value)}><option value="">Выберите определение</option>{model.definitions.map(d => <option key={skillKey(d)} value={skillKey(d)}>{d.title} · {KIND_LABELS[d.kind]} · v{d.version}</option>)}</select></Field>
   <Field title="Узел деятельности"><select required className={selectClass} value={linkNode} onChange={e => { setLinkNode(e.target.value); setEvidenceIds([]); }}><option value="">Выберите узел</option>{nodes.map(n => <option key={n.id} value={n.id}>{n.title}</option>)}</select></Field>
   <fieldset className="space-y-2"><legend className="text-sm">Основания</legend>{currentNode?.evidence?.filter(e => e.status !== "rejected").map(e => <label key={e.id} className="flex gap-2 rounded border p-2 text-sm"><input type="checkbox" checked={evidenceIds.includes(e.id)} onChange={event => setEvidenceIds(event.target.checked ? [...evidenceIds, e.id] : evidenceIds.filter(id => id !== e.id))}/><span>«{e.quote}»</span></label>)}</fieldset>
   <Field title="Вид основания"><select className={selectClass} value={basis} onChange={e => setBasis(e.target.value)}>{Object.entries(basisLabel).map(([id, label]) => <option key={id} value={id}>{label}</option>)}</select></Field><Field title="Почему цитата поддерживает эту связь"><Textarea required maxLength={4000} value={reason} onChange={e => setReason(e.target.value)}/></Field><Button disabled={!editable} type="submit">Добавить связь для проверки</Button>
  </form></details></>}
  <section className="space-y-3"><h3 className="font-medium">Основания и решения</h3>{model.links.length === 0 && <p className="text-sm text-muted-foreground">Связей с задачами пока нет.</p>}{model.links.map(link => { const d = model.definitions.find(d => d.id === link.definition_id && d.version === link.version); const n = nodes.find(n => n.id === link.node_id); const stale = link.stale || link.evidence?.some(e => !n?.evidence?.some(current => current.id === e.id && current.quote === e.quote && current.source_id === e.source_id && current.ref_id === e.ref_id && current.claim_origin === e.claim_origin && current.status !== "rejected")); return <article className="space-y-2 rounded-lg border p-3" key={link.id}><p className="font-medium">{d?.title} · v{link.version} → {n?.title ?? "Узел недоступен"}</p><p className="text-sm">{basisLabel[link.basis]} · {statusLabel[link.status]}</p>{stale && <p role="status" className="text-sm text-destructive">Основание изменилось или стало недоступно. Требуется повторная проверка.</p>}<p className="text-sm">{link.reason}</p><ul className="space-y-1 text-sm">{(link.evidence?.length ? link.evidence : n?.evidence?.filter(e => link.evidence_ids.includes(e.id)) ?? []).map(e => <li key={e.id}>«{e.quote}»</li>)}</ul>{editable && <div className="flex flex-wrap gap-2">{Object.entries(statusLabel).map(([status, label]) => <Button variant="outline" disabled={status === "approved" && (!d || skillGaps(d).length > 0 || stale)} key={status} onClick={() => change({ ...model, links: model.links.map(l => l.id === link.id ? { ...l, status } : l) })}>{label}</Button>)}<Button variant="outline" onClick={() => change({ ...model, links: model.links.filter(l => l.id !== link.id) })}>Удалить связь</Button></div>}</article>; })}</section>
  <section className="space-y-3 rounded-lg border p-4"><h3 className="font-medium">Сравнить определения</h3><p className="text-sm text-muted-foreground">Сравнение учитывает согласованные поля и обязательные способы применения инструментов. Роль и совпадение названия технологии не доказывают эквивалентность.</p><div className="grid gap-3 sm:grid-cols-2">{([['Ожидаемое определение', expected, setExpected], ['Сопоставляемое определение', actual, setActual]] as const).map(([label, value, setter]) => <Field key={label} title={label}><select className={selectClass} value={value} onChange={e => setter(e.target.value)}><option value="">Выберите ЗУН</option>{model.definitions.map(d => <option key={skillKey(d)} value={skillKey(d)}>{d.title} · v{d.version}</option>)}</select></Field>)}</div><Button variant="outline" disabled={busy || dirty || historical || !expected || !actual} onClick={() => void compare()}>Сравнить сохранённые определения</Button>{comparison && <div role="status"><p>{({ full: "Полное соответствие описаний", partial: "Частичное соответствие", not_established: "Соответствие не установлено" } as Record<string, string>)[comparison.status]}</p><ul className="list-inside list-disc text-sm">{comparison.reasons.map(reason => <li key={reason}>{reason}</li>)}</ul></div>}</section>
 </section>;
}
