export namespace client {
	
	export class Result {
	    path: string;
	    offset: number;
	    snippet: string;
	    score: number;
	    sources: string[];
	
	    static createFrom(source: any = {}) {
	        return new Result(source);
	    }
	
	    constructor(source: any = {}) {
	        if ('string' === typeof source) source = JSON.parse(source);
	        this.path = source["path"];
	        this.offset = source["offset"];
	        this.snippet = source["snippet"];
	        this.score = source["score"];
	        this.sources = source["sources"];
	    }
	}
	export class Status {
	    files: number;
	    chunks: number;
	    embedded_chunks: number;
	
	    static createFrom(source: any = {}) {
	        return new Status(source);
	    }
	
	    constructor(source: any = {}) {
	        if ('string' === typeof source) source = JSON.parse(source);
	        this.files = source["files"];
	        this.chunks = source["chunks"];
	        this.embedded_chunks = source["embedded_chunks"];
	    }
	}

}

