import { requestApi, type ActivityNode, type Evidence } from "@/lib/api/client";
export interface SkillTool {
    id: string;
    name: string;
    kind: string;
    aliases: string[];
}
export interface SkillToolUse {
    tool_id: string;
    usage: string;
    required: boolean;
    version?: string;
}
export interface SkillDefinition {
    id: string;
    version: number;
    title: string;
    kind: "knowledge" | "skill" | "proficiency";
    action: string;
    object: string;
    conditions: string;
    result: string;
    criterion: string;
    autonomy: string;
    scope: string;
    tools: SkillToolUse[];
}
export interface SkillLink {
    id: string;
    node_id: string;
    definition_id: string;
    version: number;
    evidence_ids: string[];
    evidence: Evidence[];
    basis: string;
    reason: string;
    status: string;
    stale?: boolean;
}
export interface SkillModel {
    revision: number;
    tools: SkillTool[];
    definitions: SkillDefinition[];
    links: SkillLink[];
}
export interface SkillComparison {
    status: string;
    reasons: string[];
}
export const skillKey = (d: {
    id: string;
    version: number;
}) => `${d.id}@${d.version}`;
const base = (run: string) => `/runs/${encodeURIComponent(run)}/skill-model`;
export const loadSkillModel = (run: string, revision?: number) => requestApi<SkillModel>(base(run) + (revision ? `?revision=${revision}` : ""), {}, true);
export const loadSkillNodes = (run: string) => requestApi<ActivityNode[]>(base(run) + "/nodes", {}, true);
export const saveSkillModel = (run: string, model: SkillModel) => requestApi<SkillModel>(base(run), { method: "PUT", headers: { "Content-Type": "application/json" }, body: JSON.stringify(model) }, true);
export const extractSkills = (run: string) => requestApi<SkillModel>(base(run) + "/extract", { method: "POST" }, true);
export const validateSkillModel = (run: string, model: SkillModel) => requestApi<SkillModel>(base(run) + "/validate", {method:"POST",headers:{"Content-Type":"application/json"},body:JSON.stringify(model)}, true);
export const compareSkills = (run: string, a: SkillDefinition, b: SkillDefinition) => requestApi<SkillComparison>(base(run) + "/compare", { method: "POST", headers: { "Content-Type": "application/json" }, body: JSON.stringify({ expected: { definition_id: a.id, version: a.version }, actual: { definition_id: b.id, version: b.version } }) }, true);
export const KIND_LABELS = { knowledge: "Знание", skill: "Умение", proficiency: "Владение" };
export function skillGaps(d: SkillDefinition): string[] {
    const gaps = ([['action', 'действие'], ['object', 'объект'], ['conditions', 'условия'], ['result', 'результат'], ['criterion', 'критерий']] as const).filter(([key]) => !d[key].trim()).map(([, label]) => String(label));
    if (d.kind === "proficiency") {
        if (!d.autonomy.trim())
            gaps.push("самостоятельность");
        if (!d.scope.trim())
            gaps.push("диапазон ситуаций и устойчивость");
    }
    return gaps;
}
