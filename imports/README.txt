Place kam.csv and stokomak.csv here for the scheduled importer.
Required columns: name,price_mkd
Optional: description,regular_price_mkd,available,source_updated_at
Example row format (ILLUSTRATIVE, NOT A REAL OFFER):
name,price_mkd,description,available,source_updated_at
"TEST МЛЕКО 3.2% 1Л",70.00,"ТРАЈНО МЛЕКО",true,2026-10-11

File updates should be atomic: write kam.csv.tmp, then rename to kam.csv.
Never label these imports a complete catalog unless your feed really is complete.
