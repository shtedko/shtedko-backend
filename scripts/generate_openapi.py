#!/usr/bin/env python3
"""Regenerate openapi.yaml and openapi.json. Requires PyYAML: pip install PyYAML.
Source of truth is the Go server handlers; keep this contract in sync with them.
"""
import json
from pathlib import Path
import yaml

ROOT = Path(__file__).resolve().parent.parent


def ref(name): return {'$ref': f'#/components/schemas/{name}'}
def prop(t, description=None, **extra):
    d = {'type': t}
    if description: d['description'] = description
    d.update(extra)
    return d
def obj(properties, required=None, description=None, **extra):
    d = {'type': 'object', 'properties': properties}
    if required: d['required'] = required
    if description: d['description'] = description
    d.update(extra)
    return d
def array(item, **extra): return {'type': 'array', 'items': item, **extra}
def string(**extra): return prop('string', **extra)
def integer(**extra): return prop('integer', format='int64', **extra)
def number(**extra): return prop('number', format='double', **extra)
def boolean(**extra): return prop('boolean', **extra)
def dtt(**extra): return string(format='date-time', **extra)
def pr(name, location='query', schema=None, description=None, required=False, **extra):
    p={'name': name, 'in': location, 'required': required, 'schema': schema or string()}
    if description: p['description']=description
    p.update(extra)
    return p

def response(schema=None, description='Successful response', code='200'):
    d={'description': description}
    if schema is not None: d['content']={'application/json': {'schema': schema}}
    return {code: d}
def errors(*codes):
    names={'400':'Invalid query or JSON body', '401':'Authorization required or invalid', '404':'Resource not found', '409':'Conflict or request already in progress', '429':'Too many requests', '500':'Internal server error', '502':'Upstream collection failed', '503':'Service or operation unavailable'}
    return {str(i): {'description': names.get(str(i), 'Error'), 'content':{'application/json': {'schema':ref('APIError')}}} for i in codes}
def op(tag, summary, operation_id, schema=None, params=None, body=None, body_required=True, codes=(400,500), description=None, security=None, status='200', success_description=None):
    d={'tags': [tag], 'summary': summary, 'operationId': operation_id}
    if description: d['description']=description
    if params: d['parameters']=params
    if body:
        d['requestBody']={'required':body_required,'content':{'application/json': {'schema':ref(body)}}}
    if status=='204': d['responses']={'204': {'description': success_description or 'Deleted successfully; no response body'}}
    else: d['responses']=response(ref(schema) if isinstance(schema, str) else schema, success_description or 'Successful response', status)
    d['responses'].update(errors(*codes))
    if security: d['security']=[{security:[]}]
    return d

text_id = string(description='Opaque application-defined ID. Do not infer its structure.')
amount = number(description='Quantity in base units; g, ml or pcs', exclusiveMinimum=True, minimum=0, maximum=100000000)
nullable_int = integer(nullable=True)
nullable_num = number(nullable=True)

S={}
S['APIError']=obj({'error': string(example='invalid request')}, ['error'])
S['Health']=obj({'status': string(example='ok')}, ['status'])
S['Market']=obj({'market':string(example='tinex'),'stores':integer(),'active_products':integer()})
S['MarketList']=obj({'items':array(ref('Market'))}, ['items'])
S['Store']=obj({'id':string(example='tinex-1'),'market':string(example='tinex'),'name':string(),'source_url':string(nullable=True),'status':string(description='Ingestion health or current last-sync state.'),'last_success':string(nullable=True,description='May be absent or null until the first successful crawl.'),'last_error':string(nullable=True),'active_products':integer()}, ['id','market','name','active_products'])
S['StoreList']=obj({'items':array(ref('Store'))}, ['items'])
S['CategoryCount']=obj({'category':string(example='milk'),'products':integer(),'stores':integer()}, ['category','products','stores'])
S['CategoryList']=obj({'items':array(ref('CategoryCount'))}, ['items'])
S['Product']=obj({
    'id':text_id,'name':string(example='Млеко УХТ 3.2% 1Л'),'description':string(nullable=True),'category':string(example='milk'),
    'comparison_key':string(nullable=True,description='Rule-generated, unverified equivalence candidate; not a verified identical SKU.'),
    'size_value':number(nullable=True),'size_unit':string(nullable=True,description='Normalized size unit, typically g, ml, pcs.'),
    'price_mkd':number(description='Displayed price in Macedonian denars. For financial computations prefer quote *_minor integer fields.'),
    'regular_price_mkd':nullable_num,'currency':string(enum=['MKD']),
    'available':boolean(nullable=True,description='Null means unknown availability.'),
    'source_url':string(nullable=True),'source_updated_at':string(nullable=True,description='Source timestamp/date as supplied by retailer (format varies).'),
    'last_seen_at':string(nullable=True),'active':boolean(),'store_id':string(),'store_name':string(),'market':string()
}, ['id','name','category','price_mkd','currency','store_id','store_name','market'])
S['ProductList']=obj({'items':array(ref('Product')),'total':integer(),'limit':integer(),'offset':integer()},['items','total','limit','offset'])
S['PriceObservation']=obj({'price_minor':integer(description='Price in 1/100 MKD'),'price_mkd':number(),'observed_at':string(),'store_id':string()}, ['price_minor','price_mkd','observed_at','store_id'])
S['PriceHistory']=obj({'product_id':string(),'currency':string(enum=['MKD']),'observations':array(ref('PriceObservation')),'note':string()},['product_id','currency','observations','note'])
S['ComparisonGroup']=obj({'comparison_key':string(),'category':string(),'products':integer(),'markets':integer(),'min_price_mkd':number(),'max_price_mkd':number()},['comparison_key','category','products','markets','min_price_mkd','max_price_mkd'])
S['ComparisonGroups']=obj({'match_quality':string(enum=['candidate_only_not_verified_identical_skus']), 'items':array(ref('ComparisonGroup'))}, ['match_quality','items'])
S['BasketItem']=obj({'id':string(description='Server-generated in saved baskets; optional in stateless quotes.'), 'product_id':string(description='Retailer-specific listing ID; use with quantity only.'), 'comparison_key':string(description='Unverified comparable group; use with amount and unit only.'),'quantity':integer(minimum=1,maximum=1000,description='Number of whole retail packs for product_id.'),'amount':amount,'unit':string(enum=['g','ml','pcs'])}, description='Choose exactly one: product_id+quantity, OR comparison_key+amount+unit. When using comparison_key, set allow_candidate_matches=true.')
S['NewBasketItem']=obj({'product_id':string(),'comparison_key':string(),'quantity':integer(minimum=1,maximum=1000),'amount':amount,'unit':string(enum=['g','ml','pcs'])}, description='Specify product_id + quantity or comparison_key + amount + unit; do not send id.', additionalProperties=False)
S['PatchBasketItem']=obj({'quantity':integer(minimum=1,maximum=1000),'amount':amount,'unit':string(enum=['g','ml','pcs'])}, description='Only applicable fields of an existing basket line. At least one field should be supplied.', minProperties=1, additionalProperties=False)
quote_options={
   'markets':array(string(),maxItems=30,description='Allowed market codes; empty means all.'),
   'store_ids':array(string(),maxItems=100,description='Allowed store IDs; empty means all.'),
   'allow_candidate_matches':boolean(default=False,description='Required opt-in for unverified comparison_key groups and candidate substitutions.'),
   'include_unknown_availability':boolean(default=False,description='Treat unknown-stock listings as eligible; never includes known out-of-stock.'),
   'max_age_hours':integer(minimum=0,maximum=87600,default=168,description='0 disables freshness limit.'),
   'max_stores':integer(minimum=0,maximum=3,default=0,description='0 unlimited, 1–3 limits visits. If constrained, at most 40 eligible stores supported.')
}
S['QuoteOptions']=obj(quote_options,description='Options for saved basket quotes; items are loaded from the saved basket.',additionalProperties=False)
S['QuoteRequest']=obj({'items':array(ref('BasketItem'),minItems=1,maxItems=50),**quote_options},['items'], description='Calculate least-cost whole-package basket; prices are indicative and cross-brand groups are unverified.',additionalProperties=False,example={'items':[{'comparison_key':'milk|unspecified|uht|3.2|plain|normal','amount':2000,'unit':'ml'},{'comparison_key':'bread|white','amount':500,'unit':'g'}],'allow_candidate_matches':True,'max_stores':2})
S['QuoteLine']=obj({
    'item_id':string(),'product_id':string(),'store_id':string(),'store_name':string(),'market':string(), 'product_name':string(),
    'match_quality':string(enum=['exact_retailer_listing','unverified_comparison_group']), 'packs':integer(),
    'pack_size':number(),'unit':string(),'requested_amount':number(),'purchased_amount':number(),
    'unit_price_minor':integer(description='Price for one retail pack in 1/100 MKD'),
    'unit_price_basis':string(description='kg, l, or pc when available.'), 'normalized_unit_price_mkd':number(),
    'line_total_minor':integer(description='Actual cost of whole packs, in 1/100 MKD'), 'line_total_mkd':number(),
    'source_updated_at':string(),'last_seen_at':string(),'availability':string(enum=['in_stock','out_of_stock','unknown'])
}, ['item_id','product_id','store_id','store_name','market','product_name','match_quality','packs','unit_price_minor','line_total_minor','line_total_mkd','last_seen_at','availability'])
S['StoreQuote']=obj({
    'store_id':string(),'store_name':string(),'market':string(),'complete':boolean(),
    'covered_items':integer(),'required_items':integer(),'missing_items':array(string(),nullable=True),
    'total_minor':nullable_int,'total_mkd':nullable_num, 'partial_total_minor':integer(), 'lines':array(ref('QuoteLine'))
}, ['store_id','store_name','market','complete','covered_items','required_items','missing_items','total_minor','total_mkd','partial_total_minor','lines'],description='Only use total_minor/total_mkd when complete=true. Missing items give null totals.')
S['BasketResult']=obj({'complete':boolean(),'total_minor':nullable_int,'total_mkd':nullable_num,'stores':integer(),'missing_items':array(string(),nullable=True),'lines':array(ref('QuoteLine'),nullable=True)},['complete','total_minor','total_mkd','stores','missing_items','lines'],description='Split-basket result. Incomplete baskets have null totals.')
S['QuoteResponse']=obj({'currency':string(enum=['MKD']),'calculated_at':dtt(),'match_policy':string(enum=['exact_retailer_listing_only','includes_unverified_comparison_groups']), 'freshness_limit_hours':integer(),'cheapest_single_store':{'type':'object','nullable':True,'allOf':[ref('StoreQuote')]} , 'cheapest_split_basket':ref('BasketResult'),'store_options':array(ref('StoreQuote')),'savings_minor':integer(description='Only set when single and split quotes are both complete.'),'warnings':array(string())}, ['currency','calculated_at','match_policy','freshness_limit_hours','cheapest_single_store','cheapest_split_basket','store_options','warnings'])
S['ComparisonOffers']=obj({'comparison_key':string(),'match_quality':string(enum=['unverified_comparison_group']),'requested_amount':number(),'unit':string(enum=['ml','g','pcs']), 'currency':string(enum=['MKD']),'items':array(ref('QuoteLine')),'count':integer(),'notice':string()},['comparison_key','match_quality','requested_amount','unit','currency','items','count','notice'])
S['CreatedBasket']=obj({'id':string(),'basket_token':string(nullable=True,description='Only for anonymous baskets.'),'owner':string(enum=['anonymous','account']),'created_at':dtt(),'note':string(nullable=True)},['id','owner','created_at'])
S['Credentials']=obj({'email':string(format='email',maxLength=254),'password':string(minLength=12,maxLength=1024,format='password')},['email','password'],additionalProperties=False)
S['Account']=obj({'id':string(),'email':string(format='email'),'created_at':dtt()},['id','email','created_at'])
S['AuthSession']=obj({'access_token':string(description='Opaque secret, only returned at login or registration.'),'token_type':string(enum=['Bearer']),'expires_at':dtt(),'expires_in':integer(),'user':ref('Account')},['access_token','token_type','expires_at','expires_in'])
S['MyBasketSummary']=obj({'id':string(),'created_at':dtt(),'updated_at':dtt(),'items':integer()},['id','created_at','updated_at','items'])
S['MyBaskets']=obj({'items':array(ref('MyBasketSummary'))},['items'])
S['ClaimedBasket']=obj({'id':string(),'owner':string(enum=['account'])},['id','owner'])
S['SavedBasket']=obj({'id':string(),'items':array(ref('BasketItem')),'count':integer()},['id','items','count'])
S['SyncRun']=obj({'id':integer(),'store_id':string(),'status':string(),'rows_count':integer(nullable=True),'started_at':string(),'finished_at':string(nullable=True),'error':string(nullable=True)})
S['SyncRuns']=obj({'items':array(ref('SyncRun'))}, ['items'])
S['SyncResult']=obj({'status':string(enum=['completed']),'store':string(description='Empty string if all configured stores synchronized.')},['status','store'])

id_param=pr('id','path',string(),required=True,description='Opaque identifier')
item_id_param=pr('item_id','path',string(),required=True,description='Saved-basket item ID')
limit200=pr('limit',schema=integer(minimum=1,maximum=200,default=50))
max_age=pr('max_age_hours',schema=integer(minimum=0,maximum=87600,default=168), description='0 disables price freshness filter.')

P={}
P['/healthz']={'get':op('Health','Database health probe','getHealth',schema='Health',codes=(503,))}
P['/v1/markets']={'get':op('Catalog','List supermarket chains','listMarkets','MarketList',codes=(500,))}
P['/v1/stores']={'get':op('Catalog','List physical store sources and sync status','listStores','StoreList',codes=(500,))}
P['/v1/categories']={'get':op('Catalog','List detected grocery categories','listCategories','CategoryList',codes=(500,))}
P['/v1/products']={'get':op('Catalog','Search and filter retail product listings','listProducts','ProductList', params=[
    pr('category',schema=string(example='milk')),pr('market',schema=string(example='kam')),
    pr('store',schema=string(),description='Store ID, not supermarket chain.'),pr('q',schema=string(),description='Name search with normalized Macedonian/Latin matching.'),
    pr('available',schema=boolean()),pr('include_inactive',schema=boolean(default=False)),
    limit200,pr('offset',schema=integer(minimum=0,maximum=1000000,default=0))],codes=(400,500))}
P['/v1/products/{id}']={'get':op('Catalog','Get one retailer listing','getProduct','Product',params=[id_param],codes=(404,500))}
P['/v1/products/{id}/price-history']={'get':op('Prices','Observed price change history','getPriceHistory','PriceHistory',params=[id_param,pr('limit',schema=integer(minimum=1,maximum=500,default=100))],codes=(400,500),description='History includes observed changed snapshots, not a guarantee of checkout pricing on historic dates.')}
P['/v1/comparison-groups']={'get':op('Comparison','Discover unverified product comparison groups','listComparisonGroups','ComparisonGroups', params=[pr('category',schema=string(example='milk')),pr('min_markets',schema=integer(minimum=1,maximum=50,default=2)),limit200],codes=(400,500),description='Comparison keys are heuristically generated; do not imply SKU identity.')}
P['/v1/comparisons']={'get':op('Comparison','Compare offers for a requested physical amount','compareOffers','ComparisonOffers',params=[
    pr('comparison_key',required=True,schema=string(maxLength=300),description='Candidate comparison key from /v1/comparison-groups.'),
    pr('amount',required=True,schema=number(minimum=0,exclusiveMinimum=True,maximum=100000000),description='Physical amount requested.'),
    pr('unit',required=True,schema=string(enum=['g','ml','pcs'])),max_age,
    pr('include_unknown_availability',schema=boolean(default=False)),
    pr('markets',schema=string(),description='Comma-separated market codes, e.g. tinex,kam.'),
    pr('store_ids',schema=string(),description='Comma-separated physical store IDs.')
],codes=(400,500),description='Ranked by whole pack checkout cost, not by a fractional price; offers belong to unverified candidate groups.')}
P['/v1/basket/quote']={'post':op('Baskets','Calculate the cheapest single-store and split-store basket','quoteBasket','QuoteResponse',body='QuoteRequest',codes=(400,),description='Stateless quote. Exact listings or opt-in unverified comparison groups. Null totals denote incomplete baskets. Travel costs are not considered.')}
P['/v1/auth/register']={'post':op('Authentication','Register an account and issue a bearer token','registerAccount','AuthSession',body='Credentials',status='201',codes=(400,409,429,500),description='Password must be at least 12 UTF-8 bytes. Account is not email verified.')}
P['/v1/auth/login']={'post':op('Authentication','Log in and issue a bearer token','login','AuthSession',body='Credentials',codes=(400,401,429,500))}
P['/v1/auth/logout']={'post':op('Authentication','Revoke current bearer session','logout',security='UserBearer',status='204',codes=(401,500))}
P['/v1/auth/me']={'get':op('Authentication','Get current account','currentAccount','Account',security='UserBearer',codes=(401,500))}
P['/v1/me/baskets']={'get':op('Saved baskets','List baskets owned by the logged-in user','listOwnBaskets','MyBaskets',security='UserBearer',codes=(401,500))}
P['/v1/baskets/{id}/claim']={'post':op('Saved baskets','Claim a legacy anonymous basket with basket token','claimBasket','ClaimedBasket',params=[id_param],codes=(401,404,409,500),description='Requires both Authorization: Bearer and X-Basket-Token.')}
P['/v1/baskets/{id}/claim']['post']['security']=[{'UserBearer':[],'BasketToken':[]}]
P['/v1/baskets']={'post':op('Saved baskets','Create an account-owned or anonymous basket','createBasket','CreatedBasket',status='201',codes=(401,500,),description='With bearer token creates account-owned basket; without bearer token creates legacy anonymous basket and returns X-Basket-Token.')}
P['/v1/baskets']['post']['security']=[{}, {'UserBearer':[]}]
P['/v1/baskets/{id}']={
    'get':op('Saved baskets','Read saved basket','getBasket','SavedBasket',params=[id_param],security='BasketToken',codes=(401,404,500)),
    'delete':op('Saved baskets','Delete saved basket and its items','deleteBasket',params=[id_param],status='204',security='BasketToken',codes=(401,404,500))}
P['/v1/baskets/{id}/items']={'post':op('Saved baskets','Add one saved basket line','addBasketItem','BasketItem',params=[id_param],body='NewBasketItem',security='BasketToken',status='201',codes=(400,401,404,409,500))}
P['/v1/baskets/{id}/items/{item_id}']={
    'patch':op('Saved baskets','Update saved basket line amount or quantity','updateBasketItem','BasketItem',params=[id_param,item_id_param],body='PatchBasketItem',security='BasketToken',codes=(400,401,404,500)),
    'delete':op('Saved baskets','Remove saved basket line','deleteBasketItem',params=[id_param,item_id_param],status='204',security='BasketToken',codes=(401,404,500))}
P['/v1/baskets/{id}/quote']={'post':op('Saved baskets','Optimize a saved basket','quoteSavedBasket','QuoteResponse',params=[id_param],body='QuoteOptions',security='BasketToken',codes=(400,401,404),description='Body is required even if empty ({}). Items are taken from the saved basket, not supplied here.')}
P['/v1/sync-runs']={'get':op('Operations','Recent collection job outcomes','listSyncRuns','SyncRuns',params=[pr('limit',schema=integer(minimum=1,maximum=200,default=30))],codes=(400,500))}
P['/v1/admin/sync']={'post':op('Operations','Run collection on demand','triggerSync','SyncResult',params=[pr('store',schema=string(),description='Optional configured physical store ID; absent triggers all stores.')],security='AdminBearer',codes=(401,404,409,502,503),description='This may take a long time depending on retailer source size; disable if ADMIN_TOKEN is not configured.')}

# Account-owned baskets use UserBearer; old anonymous baskets use BasketToken.
for route in ['/v1/baskets/{id}','/v1/baskets/{id}/items','/v1/baskets/{id}/items/{item_id}','/v1/baskets/{id}/quote']:
    for endpoint in P[route].values():
        endpoint['security']=[{'UserBearer':[]},{'BasketToken':[]}]

# Keep key order for human-readable YAML review.
spec={
    'openapi':'3.0.3',
    'info': {
        'title': 'Grocery Multi-Store Price Comparison API',
        'version': '1.2.0',
        'description': 'REST API for supermarket prices, rule-based candidate product comparisons and complete-basket optimization. Prices are estimates and retailer crawling completeness is not guaranteed. All values are Macedonian denar (MKD); integer *_minor fields denote hundredths of one MKD. Group matches are NOT verified identical products.'
    },
    'servers':[{'url':'/', 'description':'Same-origin deployment (replace with an HTTPS URL when generating a production SDK)'}],
    'tags':[{'name':name,'description':desc} for name,desc in [
        ('Health','Service readiness'),('Catalog','Stores and product listings'),('Prices','Observed historical prices'),('Comparison','Unverified equivalent-product candidates'),('Baskets','Anonymous basket quotes'),('Authentication','Register, log in and manage user sessions'),('Saved baskets','Account-owned and legacy token-protected basket CRUD'),('Operations','Synchronization health and control')]],
    'paths': P,
    'components':{
        'securitySchemes':{
            'UserBearer': {'type':'http','scheme':'bearer','bearerFormat':'opaque','description':'User session returned by register/login. Store in OS secure storage; expires after 7 days.'},
            'BasketToken': {'type':'apiKey','in':'header','name':'X-Basket-Token','description':'Per-basket secret returned only once from POST /v1/baskets. Do not send as bearer token.'},
            'AdminBearer':{'type':'http','scheme':'bearer','description':'ADMIN_TOKEN environment variable; only required for manual synchronization.'}
        },
        'schemas': S
    }
}

# Rewrite only when content changes so rebuilds are reproducible.
class NoAliasDumper(yaml.SafeDumper):
    def ignore_aliases(self, data):
        return True

yaml_text = yaml.dump(spec,Dumper=NoAliasDumper,sort_keys=False,allow_unicode=True,width=105)
json_text = json.dumps(spec,ensure_ascii=False,indent=2) + '\n'
(ROOT/'openapi.yaml').write_text(yaml_text,encoding='utf-8')
(ROOT/'openapi.json').write_text(json_text,encoding='utf-8')
print(f"Wrote {ROOT/'openapi.yaml'} and {ROOT/'openapi.json'}: {len(P)} paths, {sum(map(len,P.values()))} operations, {len(S)} schemas")
