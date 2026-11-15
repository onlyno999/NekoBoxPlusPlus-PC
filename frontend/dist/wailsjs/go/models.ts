export namespace main {
	
	export class GroupDTO {
	    id: number;
	    name: string;
	    url: string;
	    updatedAt: number;
	    autoUpdate: boolean;
	    autoDelayMin: number;
	    nodeCount: number;
	
	    static createFrom(source: any = {}) {
	        return new GroupDTO(source);
	    }
	
	    constructor(source: any = {}) {
	        if ('string' === typeof source) source = JSON.parse(source);
	        this.id = source["id"];
	        this.name = source["name"];
	        this.url = source["url"];
	        this.updatedAt = source["updatedAt"];
	        this.autoUpdate = source["autoUpdate"];
	        this.autoDelayMin = source["autoDelayMin"];
	        this.nodeCount = source["nodeCount"];
	    }
	}
	export class NodeDTO {
	    id: string;
	    groupId: number;
	    name: string;
	    type: string;
	    region: string;
	    latency: number;
	    hasLatency: boolean;
	    up: number;
	    down: number;
	    selected: boolean;
	
	    static createFrom(source: any = {}) {
	        return new NodeDTO(source);
	    }
	
	    constructor(source: any = {}) {
	        if ('string' === typeof source) source = JSON.parse(source);
	        this.id = source["id"];
	        this.groupId = source["groupId"];
	        this.name = source["name"];
	        this.type = source["type"];
	        this.region = source["region"];
	        this.latency = source["latency"];
	        this.hasLatency = source["hasLatency"];
	        this.up = source["up"];
	        this.down = source["down"];
	        this.selected = source["selected"];
	    }
	}
	export class RuleDTO {
	    name: string;
	    content: string;
	    outbound: string;
	    poolGroupId: number;
	    poolCc: string;
	    enabled: boolean;
	
	    static createFrom(source: any = {}) {
	        return new RuleDTO(source);
	    }
	
	    constructor(source: any = {}) {
	        if ('string' === typeof source) source = JSON.parse(source);
	        this.name = source["name"];
	        this.content = source["content"];
	        this.outbound = source["outbound"];
	        this.poolGroupId = source["poolGroupId"];
	        this.poolCc = source["poolCc"];
	        this.enabled = source["enabled"];
	    }
	}
	export class StateDTO {
	    running: boolean;
	    mode: string;
	    mixedPort: number;
	    selectedId: string;
	    autoGroupId: number;
	    autoCc: string;
	    groups: GroupDTO[];
	    nodes: NodeDTO[];
	    rules: RuleDTO[];
	    builtinPlay: boolean;
	    builtinCn: boolean;
	    upSpeed: number;
	    downSpeed: number;
	    lastError: string;
	
	    static createFrom(source: any = {}) {
	        return new StateDTO(source);
	    }
	
	    constructor(source: any = {}) {
	        if ('string' === typeof source) source = JSON.parse(source);
	        this.running = source["running"];
	        this.mode = source["mode"];
	        this.mixedPort = source["mixedPort"];
	        this.selectedId = source["selectedId"];
	        this.autoGroupId = source["autoGroupId"];
	        this.autoCc = source["autoCc"];
	        this.groups = this.convertValues(source["groups"], GroupDTO);
	        this.nodes = this.convertValues(source["nodes"], NodeDTO);
	        this.rules = this.convertValues(source["rules"], RuleDTO);
	        this.builtinPlay = source["builtinPlay"];
	        this.builtinCn = source["builtinCn"];
	        this.upSpeed = source["upSpeed"];
	        this.downSpeed = source["downSpeed"];
	        this.lastError = source["lastError"];
	    }
	
		convertValues(a: any, classs: any, asMap: boolean = false): any {
		    if (!a) {
		        return a;
		    }
		    if (a.slice && a.map) {
		        return (a as any[]).map(elem => this.convertValues(elem, classs));
		    } else if ("object" === typeof a) {
		        if (asMap) {
		            for (const key of Object.keys(a)) {
		                a[key] = new classs(a[key]);
		            }
		            return a;
		        }
		        return new classs(a);
		    }
		    return a;
		}
	}

}

