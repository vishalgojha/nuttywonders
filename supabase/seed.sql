-- NuttyWonders seed
-- Sources:
--   * NuttyWonders_Catalogue_.pdf          -> product names, descriptions, ingredient lists
--   * "Costing per SKU" + "Costing Mastersheet" sheets -> recipe batch quantities
--   * "Raw material masterlist" sheet       -> ingredient prices in Rs/kg
--   * Sheet1 "Cost sheet" + Sheet4         -> selling prices per pack
-- Prices below are the shop's own selling prices, so nothing changes for
-- customers. Ingredient prices are the masterlist prices; where the
-- mastersheet lumped a seasoning as a flat Rs 5 line, this seed prices the
-- actual grams instead. Review with Kavita and edit in the Studio admin.

insert into public.app_settings (key, value) values
  ('business_name',        '"NuttyWonders"'::jsonb),
  ('owner_phone_e164',     '"918108398025"'::jsonb),
  ('whatsapp_link',        '"https://wa.me/918108398025"'::jsonb),
  ('instagram',            '"https://www.instagram.com/nuttywonders/"'::jsonb),
  ('email',                '"Kavita@nuttywonders.com"'::jsonb),
  ('upi_vpa',              '""'::jsonb),
  ('payment_method',       '"razorpay"'::jsonb),
  ('delivery_fee_inr',     '60'::jsonb),
  ('free_shipping_over_inr', '799'::jsonb),
  ('support_hours',        '"10am - 7pm, Mon to Sat"'::jsonb)
on conflict (key) do nothing;

-- ---------------------------------------------------------------------------
-- raw material master (Rs per kg)
-- ---------------------------------------------------------------------------
insert into public.ingredients (name, price_per_kg_inr, suggested_brand) values
  ('Chocolate couverture',   1635,  null),
  ('Dark chocolate chips',    450,  'Morde'),
  ('2M dark chocolate compound', 2100, '2M'),
  ('Coconut oil',             724,  'KLF'),
  ('Cocoa powder',            1200, 'Hersheys'),
  ('Vanilla extract',         2500, 'Sprig / Ossoro'),
  ('Honey',                    558,  'Any organic brand'),
  ('Almond',                   800,  'Local'),
  ('Cashew',                   910,  'Local'),
  ('Pista',                   1600,  'Local'),
  ('Walnut',                  1525,  'Local'),
  ('Hazelnut',                1480,  'Local'),
  ('Cranberry (dried)',        995,  null),
  ('Dried strawberry',        2000,  null),
  ('Dried blackberry',        2000,  null),
  ('Raisin',                   500,  'Local'),
  ('Dates',                    400,  'Kimia / Mejdool'),
  ('Grated apple',             625,  null),
  ('Jaggery',                  180,  'Local'),
  ('Jaggery powder',           200,  'Local'),
  ('Green gram flour',         180,  null),
  ('Whole wheat flour',         62,  null),
  ('Ragi flour',                80,  null),
  ('Ghee',                     750,  'Amul'),
  ('Cardamom powder',         4000,  null),
  ('Cinnamon powder',         1200,  null),
  ('Rolled oats',              400,  'True Elements'),
  ('Instant oats',             190,  'Quaker'),
  ('Flax seeds',               200,  null),
  ('Chia seeds',               400,  null),
  ('Grated coconut',           440,  'Local'),
  ('Peanut butter',            200,  null),
  ('Sunflower seeds',          390,  null),
  ('Melon seeds',              930,  null),
  ('Pumpkin seeds',            760,  null),
  ('Poppy seeds',             2450,  null),
  ('Sesame seeds',             320,  null),
  ('Quinoa',                   274,  'Two Brothers / OOO Farms'),
  ('Quinoa puff',              995,  null),
  ('Makhana',                 1970,  'Local'),
  ('Chana jor garam',          180,  null),
  ('Peanut',                   168,  'Local'),
  ('Rice puff',                160,  'Local'),
  ('Bajra flakes',             119,  'Two Brothers'),
  ('Ragi flakes',              142,  'Two Brothers'),
  ('Jowar flakes',             130,  'Two Brothers'),
  ('Kodo millet flakes',      167.5, 'Two Brothers'),
  ('Jowar flour',               80,  null),
  ('Amaranth flour',           220,  null),
  ('Rice flour',                65,  null),
  ('Corn flakes',              200,  null),
  ('Tamarind paste',           280,  'Mother''s Recipe'),
  ('Protein powder',          2500,  'The Whole Truth'),
  ('Coffee powder',           3000,  'Nescafe'),
  ('Baking soda',              150,  null),
  ('Himalayan salt',            40,  null),
  ('Chaat masala',             300,  'Everest / Keya'),
  ('Amchur powder',            600,  'Urban Platter'),
  ('Red chilli powder',        400,  'Everest'),
  ('Black pepper',            1200,  'Everest'),
  ('Turmeric powder',          300,  'Everest'),
  ('Curry leaves',            1000,  'Local'),
  ('Lemon zest',              2000,  null),
  ('Orange zest',             3000,  null),
  ('Tahini',                   320,  null)
on conflict (name) do nothing;

-- ---------------------------------------------------------------------------
-- products
-- cost_labour_inr / cost_overhead_inr follow the Costing Mastersheet:
-- labour Rs 20 per unit, overhead (utilities + consumables) Rs 18 per pack.
-- ---------------------------------------------------------------------------
insert into public.products (
  slug, name, tagline, description, category, image_path,
  pack_size_g, batch_output_g, shelf_life_days,
  ingredients, allergens, price_inr,
  cost_packaging_inr, cost_labour_inr, cost_overhead_inr,
  stock_packs, sort_order
) values
  ('chocolate-granola', 'Chocolate Granola', 'Savour the purity, love the taste',
   'Crunchy rolled oats, nuts and rich cocoa baked into a deliciously wholesome breakfast and snack.',
   'granola', '/assets/products/chocolate-granola.jpg',
   200, 205, 20,
   array['Rolled oats','Cashew','Almond','Desicated coconut','Honey','Chia seeds','Vanilla extract','Kodo millet','Cocoa powder','Dark chocolate chips','Cinnamon powder'],
   array['Nuts','Dairy-free','Gluten-free'],
   320, 15, 20, 18, 24, 10),

  ('berries-granola', 'Berries Granola', 'A fruity, wholesome crunch',
   'Crispy granola infused with the goodness of berries for a fruity, wholesome crunch.',
   'granola', '/assets/products/berries-granola.jpg',
   200, 200, 20,
   array['Rolled oats','Jowar flakes','Coconut oil','Honey','Almonds','Melon seeds','Vanilla extract','Dried strawberry','Cranberry (dried)','Dried blackberry','Chia seeds'],
   array['Nuts'],
   320, 15, 20, 18, 18, 20),

  ('apple-cinnamon-granola', 'Apple Cinnamon Granola', 'Warmth in every bite',
   'A comforting blend of apples, cinnamon and crunchy oats that brings warmth to every bite.',
   'granola', '/assets/products/apple-cinnamon-granola.jpg',
   200, 200, 20,
   array['Rolled oats','Honey','Grated apple','Almonds','Coconut oil','Cinnamon powder','Vanilla extract'],
   array['Nuts'],
   320, 15, 20, 18, 20, 30),

  ('cashew-truffles', 'Cashew Truffles', 'Rich and creamy',
   'Rich, creamy truffles made with premium cashews and naturally sweetened with honey.',
   'bites', '/assets/products/cashew-truffles.jpg',
   200, 180, 20,
   array['Oats','Cashew','Almond','Grated coconut','Honey','Chia seeds','Vanilla extract'],
   array['Nuts'],
   349, 45, 20, 18, 22, 40),

  ('dates-nuts-bites', 'Dates & Nuts Bites', 'Natural sweetness, lasting energy',
   'A nourishing blend of dates, nuts and seeds that delivers natural sweetness and lasting energy.',
   'bites', '/assets/products/dates-nuts-bites.jpg',
   200, 220, 20,
   array['Dates','Cashew','Sunflower seeds','Flax seeds','Pumpkin seeds','Almond','Cinnamon powder'],
   array['Nuts'],
   319, 45, 20, 18, 31, 50),

  ('cocoa-ragi-bites', 'Cocoa Ragi Bites', 'Ragi and cocoa, nothing more',
   'A wholesome blend of ragi and cocoa, naturally crafted for a satisfying everyday snack.',
   'bites', '/assets/products/cocoa-ragi-bites.jpg',
   200, 1365, 25,
   array['Ragi flour','Almond','Cashew','Jaggery','Cocoa powder','Ghee','Cardamom powder'],
   array['Nuts'],
   249, 45, 20, 18, 26, 60),

  ('oats-bites', 'Oats Bites', 'Everyday nourishment',
   'Wholesome oats, nuts and seeds combined into a satisfying snack for everyday nourishment.',
   'bites', '/assets/products/oats-bites.jpg',
   200, 200, 20,
   array['Instant oats','Almond','Cashew','Sunflower seeds','Flax seeds','Pumpkin seeds','Cinnamon powder'],
   array['Nuts','Gluten (oats)'],
   249, 45, 20, 18, 20, 70),

  ('crunchy-munchy-snack', 'Crunchy Munchy Snack', 'Tangy, crunchy, guilty-free',
   'A crunchy medley of oats, millets, nuts and tangy spices that makes healthy snacking irresistible.',
   'bars', '/assets/products/crunchy-munchy-snack.jpg',
   200, 210, 15,
   array['Rolled oats','Peanut','Bajra flakes','Cashew','Honey','Melon seeds','Sunflower seeds','Tamarind paste','Lemon zest','Chaat masala','Amchur powder','Red chilli powder','Himalayan salt'],
   array['Nuts','Peanuts'],
   259, 15, 20, 18, 12, 80),

  ('chatpata-makhana', 'Chatpata Makhana Snack', 'Bold Indian spices, light crunch',
   'Light, crunchy makhana tossed with wholesome grains and bold Indian spices for a guilt-free snack.',
   'bars', '/assets/products/chatpata-makhana.jpg',
   210, 270, 15,
   array['Makhana','Rice puff','Chana jor garam','Jowar flakes','Cashew','Honey','Coconut oil','Pumpkin seeds','Sesame seeds','Flax seeds','Quinoa','Curry leaves','Chaat masala','Red chilli powder','Black pepper','Turmeric powder','Himalayan salt'],
   array['Nuts'],
   259, 15, 20, 18, 12, 90),

  ('millet-bar', 'Millet Bar', 'Nutritious millets, satisfying crunch',
   'A wholesome blend of nutritious millets, nuts and seeds, crafted into a satisfyingly crunchy bar.',
   'bars', '/assets/products/millet-bar.jpg',
   200, 520, 20,
   array['Rolled oats','Peanut','Bajra flakes','Cashew','Honey','Melon seeds','Sunflower seeds','Tamarind paste','Lemon zest','Chaat masala','Amchur powder','Red chilli powder','Himalayan salt'],
   array['Nuts','Peanuts'],
   299, 25, 20, 18, 15, 100),

  ('orange-chocolate-bar', 'Orange Chocolate Protein Bar', 'A balanced indulgence',
   'Rich dark chocolate infused with refreshing orange for a perfectly balanced indulgence.',
   'bars', '/assets/products/orange-chocolate-bar.jpg',
   200, 652, 30,
   array['Instant oats','Almond','Cashew','Cocoa powder','Protein powder','Dates','Orange zest','Dark chocolate chips'],
   array['Nuts','Soy/milk proteins'],
   349, 25, 20, 18, 10, 110),

  ('ragi-brownie', 'Ragi Brownie', 'Soft, fudgy, a little wiser',
   'Soft, fudgy brownies made with wholesome ingredients for a richer, more mindful indulgence.',
   'bars', '/assets/products/ragi-brownie.jpg',
   200, 539, 15,
   array['Ragi flour','Whole wheat flour','Jaggery','Cocoa powder','Milk','Flax seeds','Dark chocolate chips','Baking soda'],
   array['Gluten','Dairy','Nuts'],
   299, 20, 20, 18, 16, 120)
on conflict (slug) do nothing;

-- ---------------------------------------------------------------------------
-- recipes: grams per production batch, as written in the costing mastersheet
-- ---------------------------------------------------------------------------
insert into public.recipe_items (product_id, ingredient_id, qty_g)
select p.id, i.id, r.qty_g
from (values
  -- Chocolate Granola (205 g batch)
  ('chocolate-granola', 'Rolled oats', 90),
  ('chocolate-granola', 'Cocoa powder', 15),
  ('chocolate-granola', 'Coconut oil', 10),
  ('chocolate-granola', 'Kodo millet flakes', 20),
  ('chocolate-granola', 'Vanilla extract', 2),
  ('chocolate-granola', 'Almond', 10),
  ('chocolate-granola', 'Dark chocolate chips', 20),
  ('chocolate-granola', 'Cinnamon powder', 2),
  ('chocolate-granola', 'Honey', 20),
  ('chocolate-granola', 'Cashew', 10),
  ('chocolate-granola', 'Chia seeds', 10),
  ('chocolate-granola', 'Himalayan salt', 2),

  -- Berries Granola (200 g batch)
  ('berries-granola', 'Rolled oats', 120),
  ('berries-granola', 'Cranberry (dried)', 15),
  ('berries-granola', 'Coconut oil', 15),
  ('berries-granola', 'Honey', 15),
  ('berries-granola', 'Vanilla extract', 2),
  ('berries-granola', 'Cinnamon powder', 2),
  ('berries-granola', 'Almond', 15),
  ('berries-granola', 'Cashew', 15),
  ('berries-granola', 'Walnut', 15),
  ('berries-granola', 'Pumpkin seeds', 10),
  ('berries-granola', 'Melon seeds', 10),
  ('berries-granola', 'Chia seeds', 5),
  ('berries-granola', 'Raisin', 10),

  -- Apple Cinnamon Granola (200 g batch)
  ('apple-cinnamon-granola', 'Rolled oats', 100),
  ('apple-cinnamon-granola', 'Grated apple', 80),
  ('apple-cinnamon-granola', 'Coconut oil', 10),
  ('apple-cinnamon-granola', 'Honey', 20),
  ('apple-cinnamon-granola', 'Vanilla extract', 2),
  ('apple-cinnamon-granola', 'Almond', 20),
  ('apple-cinnamon-granola', 'Cinnamon powder', 2),
  ('apple-cinnamon-granola', 'Himalayan salt', 2),

  -- Cashew Truffles (180 g batch, 15 truffles)
  ('cashew-truffles', 'Instant oats', 25),
  ('cashew-truffles', 'Grated coconut', 10),
  ('cashew-truffles', 'Almond', 25),
  ('cashew-truffles', 'Dates', 100),
  ('cashew-truffles', 'Honey', 50),
  ('cashew-truffles', 'Vanilla extract', 2),
  ('cashew-truffles', 'Cashew', 25),

  -- Dates & Nuts Bites (220 g batch)
  ('dates-nuts-bites', 'Dates', 100),
  ('dates-nuts-bites', 'Almond', 40),
  ('dates-nuts-bites', 'Cashew', 40),
  ('dates-nuts-bites', 'Sunflower seeds', 20),
  ('dates-nuts-bites', 'Flax seeds', 10),
  ('dates-nuts-bites', 'Pumpkin seeds', 10),
  ('dates-nuts-bites', 'Cinnamon powder', 2),

  -- Cocoa Ragi Bites (1365 g batch, ~190 laddoos)
  ('cocoa-ragi-bites', 'Ragi flour', 500),
  ('cocoa-ragi-bites', 'Almond', 70),
  ('cocoa-ragi-bites', 'Cashew', 70),
  ('cocoa-ragi-bites', 'Cocoa powder', 40),
  ('cocoa-ragi-bites', 'Jaggery', 335),
  ('cocoa-ragi-bites', 'Cardamom powder', 2),
  ('cocoa-ragi-bites', 'Ghee', 350),

  -- Oats Bites (200 g batch)
  ('oats-bites', 'Instant oats', 120),
  ('oats-bites', 'Almond', 25),
  ('oats-bites', 'Cashew', 40),
  ('oats-bites', 'Grated coconut', 20),
  ('oats-bites', 'Flax seeds', 10),
  ('oats-bites', 'Dates', 25),
  ('oats-bites', 'Pumpkin seeds', 10),
  ('oats-bites', 'Cinnamon powder', 2),
  ('oats-bites', 'Himalayan salt', 2),

  -- Crunchy Munchy Snack (210 g batch)
  ('crunchy-munchy-snack', 'Rolled oats', 70),
  ('crunchy-munchy-snack', 'Peanut', 20),
  ('crunchy-munchy-snack', 'Bajra flakes', 15),
  ('crunchy-munchy-snack', 'Cashew', 15),
  ('crunchy-munchy-snack', 'Melon seeds', 15),
  ('crunchy-munchy-snack', 'Sunflower seeds', 15),
  ('crunchy-munchy-snack', 'Honey', 50),
  ('crunchy-munchy-snack', 'Tamarind paste', 10),
  ('crunchy-munchy-snack', 'Lemon zest', 2),
  ('crunchy-munchy-snack', 'Chaat masala', 3),
  ('crunchy-munchy-snack', 'Amchur powder', 2),
  ('crunchy-munchy-snack', 'Red chilli powder', 1.5),
  ('crunchy-munchy-snack', 'Himalayan salt', 1),

  -- Chatpata Makhana Snack (270 g batch)
  ('chatpata-makhana', 'Makhana', 25),
  ('chatpata-makhana', 'Rice puff', 12),
  ('chatpata-makhana', 'Cashew', 50),
  ('chatpata-makhana', 'Flax seeds', 7),
  ('chatpata-makhana', 'Pumpkin seeds', 7),
  ('chatpata-makhana', 'Sesame seeds', 15),
  ('chatpata-makhana', 'Quinoa', 20),
  ('chatpata-makhana', 'Chana jor garam', 30),
  ('chatpata-makhana', 'Coconut oil', 15),
  ('chatpata-makhana', 'Honey', 25),
  ('chatpata-makhana', 'Jowar flakes', 30),
  ('chatpata-makhana', 'Curry leaves', 2),
  ('chatpata-makhana', 'Chaat masala', 3),
  ('chatpata-makhana', 'Red chilli powder', 2),
  ('chatpata-makhana', 'Black pepper', 1),
  ('chatpata-makhana', 'Turmeric powder', 2),
  ('chatpata-makhana', 'Himalayan salt', 3),

  -- Millet Bar (520 g batch)
  ('millet-bar', 'Instant oats', 70),
  ('millet-bar', 'Almond', 100),
  ('millet-bar', 'Cocoa powder', 15),
  ('millet-bar', 'Bajra flakes', 30),
  ('millet-bar', 'Ragi flakes', 30),
  ('millet-bar', 'Dates', 160),
  ('millet-bar', 'Peanut butter', 160),
  ('millet-bar', 'Honey', 60),
  ('millet-bar', '2M dark chocolate compound', 30),

  -- Orange Chocolate Protein Bar (652 g batch)
  ('orange-chocolate-bar', 'Almond', 90),
  ('orange-chocolate-bar', 'Cashew', 115),
  ('orange-chocolate-bar', 'Instant oats', 80),
  ('orange-chocolate-bar', 'Protein powder', 80),
  ('orange-chocolate-bar', 'Cocoa powder', 27),
  ('orange-chocolate-bar', 'Dates', 240),
  ('orange-chocolate-bar', 'Orange zest', 3),
  ('orange-chocolate-bar', 'Dark chocolate chips', 20),

  -- Ragi Brownie (539 g batch)
  ('ragi-brownie', 'Ragi flour', 60),
  ('ragi-brownie', 'Whole wheat flour', 60),
  ('ragi-brownie', '2M dark chocolate compound', 200),
  ('ragi-brownie', 'Jaggery powder', 180),
  ('ragi-brownie', 'Flax seeds', 12),
  ('ragi-brownie', 'Coffee powder', 1.5),
  ('ragi-brownie', 'Cocoa powder', 13),
  ('ragi-brownie', 'Baking soda', 2.5),
  ('ragi-brownie', 'Dark chocolate chips', 10)
) as r(slug, ingredient, qty_g)
join public.products p on p.slug = r.slug
join public.ingredients i on i.name = r.ingredient
on conflict (product_id, ingredient_id) do nothing;

-- ---------------------------------------------------------------------------
-- per-pack ingredient cost, derived from the recipes above so the number in
-- the admin always matches the recipe and the ingredient master
-- ---------------------------------------------------------------------------
update public.products p
   set cost_ingredient_inr = round((
       select coalesce(sum(r.qty_g * i.price_per_kg_inr / 1000), 0)
         from public.recipe_items r
         join public.ingredients i on i.id = r.ingredient_id
        where r.product_id = p.id
     ) / nullif(p.batch_output_g, 0) * p.pack_size_g, 2);