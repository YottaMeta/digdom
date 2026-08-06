export namespace model {
	
	export class DiffItem {
	    name: string;
	    state: string;
	    tag: string;
	    ips: string[];
	    verdict: string;
	    scan_id: number;
	
	    static createFrom(source: any = {}) {
	        return new DiffItem(source);
	    }
	
	    constructor(source: any = {}) {
	        if ('string' === typeof source) source = JSON.parse(source);
	        this.name = source["name"];
	        this.state = source["state"];
	        this.tag = source["tag"];
	        this.ips = source["ips"];
	        this.verdict = source["verdict"];
	        this.scan_id = source["scan_id"];
	    }
	}
	export class DiffResult {
	    added: DiffItem[];
	    removed: DiffItem[];
	
	    static createFrom(source: any = {}) {
	        return new DiffResult(source);
	    }
	
	    constructor(source: any = {}) {
	        if ('string' === typeof source) source = JSON.parse(source);
	        this.added = this.convertValues(source["added"], DiffItem);
	        this.removed = this.convertValues(source["removed"], DiffItem);
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
	export class RecheckItem {
	    name: string;
	    ok: boolean;
	    status: number;
	    scheme: string;
	    note: string;
	
	    static createFrom(source: any = {}) {
	        return new RecheckItem(source);
	    }
	
	    constructor(source: any = {}) {
	        if ('string' === typeof source) source = JSON.parse(source);
	        this.name = source["name"];
	        this.ok = source["ok"];
	        this.status = source["status"];
	        this.scheme = source["scheme"];
	        this.note = source["note"];
	    }
	}
	export class ResultRow {
	    id: number;
	    name: string;
	    ips: string[];
	    cnames: string[];
	    tag: string;
	    base: string;
	    depth: number;
	    verdict: string;
	    note: string;
	    http_status: number;
	    http_scheme: string;
	    http_ok: boolean;
	
	    static createFrom(source: any = {}) {
	        return new ResultRow(source);
	    }
	
	    constructor(source: any = {}) {
	        if ('string' === typeof source) source = JSON.parse(source);
	        this.id = source["id"];
	        this.name = source["name"];
	        this.ips = source["ips"];
	        this.cnames = source["cnames"];
	        this.tag = source["tag"];
	        this.base = source["base"];
	        this.depth = source["depth"];
	        this.verdict = source["verdict"];
	        this.note = source["note"];
	        this.http_status = source["http_status"];
	        this.http_scheme = source["http_scheme"];
	        this.http_ok = source["http_ok"];
	    }
	}
	export class ScanSummary {
	    id: number;
	    target: string;
	    params: string;
	    started_at: number;
	    duration_ms: number;
	    queried: number;
	    hits: number;
	    wildcards: number;
	    unreviewed: number;
	    status: string;
	    error: string;
	
	    static createFrom(source: any = {}) {
	        return new ScanSummary(source);
	    }
	
	    constructor(source: any = {}) {
	        if ('string' === typeof source) source = JSON.parse(source);
	        this.id = source["id"];
	        this.target = source["target"];
	        this.params = source["params"];
	        this.started_at = source["started_at"];
	        this.duration_ms = source["duration_ms"];
	        this.queried = source["queried"];
	        this.hits = source["hits"];
	        this.wildcards = source["wildcards"];
	        this.unreviewed = source["unreviewed"];
	        this.status = source["status"];
	        this.error = source["error"];
	    }
	}

}

